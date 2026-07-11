// Package cache is prmap's on-disk response cache, keyed by the PR head SHA.
// It stores the raw /files JSON once so the tier-1 map and every future
// per-file (tier-2) read are served without re-downloading the whole diff.
//
// The cache is strictly best-effort: it depends only on the standard library,
// touches disk, and NEVER fails the command — any I/O or corruption problem
// degrades to a miss (read) or a skipped write, with at most a diagnostic line
// to the injected writer. A Store whose root could not be resolved is a valid
// no-op Store.
package cache

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// schemaVersion is the path segment that isolates the on-disk format. Bumping
// it makes every old entry unreachable (a fresh directory), so a format change
// needs zero migration.
const schemaVersion = "v1"

// hexSHARe guards the head SHA before it is used as a filename — a non-hex
// value (an API change, a corrupt response) must never reach the filesystem.
var hexSHARe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// Key identifies one cache entry. HeadSHA is the version; Host defaults to
// github.com when empty.
type Key struct {
	Host    string
	Owner   string
	Repo    string
	Number  int
	HeadSHA string
}

// Envelope is the self-contained on-disk record. Files holds gh's exact merged
// --paginate /files array (patch, blob_url, previous_filename preserved) so
// tier 2 re-reads the same bytes with no refetch. The other fields make the
// entry self-describing and let a read validate the head SHA against its own
// filename.
type Envelope struct {
	Schema       int             `json:"schema"`
	Tool         string          `json:"tool"`
	Host         string          `json:"host"`
	Owner        string          `json:"owner"`
	Repo         string          `json:"repo"`
	PR           int             `json:"pr"`
	HeadSHA      string          `json:"head_sha"`
	BaseSHA      string          `json:"base_sha"`
	ChangedFiles int             `json:"changed_files"`
	Additions    int             `json:"additions"`
	Deletions    int             `json:"deletions"`
	FetchedAt    string          `json:"fetched_at"`
	Files        json.RawMessage `json:"files"`
}

// Store is the cache handle. A zero-value or unresolvable-root Store is a no-op.
type Store struct {
	root string    // "" => no-op
	diag io.Writer // nil => io.Discard
}

// Open resolves the cache root — PRMAP_CACHE_DIR (absolute) if set, else
// os.UserCacheDir()/prmap — and returns a Store. If no root can be resolved it
// returns a no-op Store and notes it on diag; a cache failure must never fail
// the command. diag receives cache diagnostics (a real writer under --verbose,
// io.Discard otherwise).
func Open(diag io.Writer) *Store {
	if diag == nil {
		diag = io.Discard
	}
	s := &Store{diag: diag}
	if env := os.Getenv("PRMAP_CACHE_DIR"); env != "" {
		if filepath.IsAbs(env) {
			s.root = env
			return s
		}
		fmt.Fprintf(diag, "prmap: ignoring non-absolute PRMAP_CACHE_DIR %q; cache disabled\n", env)
		return s
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintf(diag, "prmap: no cache directory available (%v); cache disabled\n", err)
		return s
	}
	s.root = filepath.Join(dir, "prmap")
	return s
}

// Get returns the cached /files bytes for k, or (nil, false) on any miss. A
// present-but-unreadable, unparseable, or head-SHA-mismatched entry is treated
// as a miss and best-effort deleted so the next fetch repopulates it.
func (s *Store) Get(k Key) (json.RawMessage, bool) {
	path, ok := s.pathFor(k)
	if !ok {
		return nil, false
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is confined to the cache root, every segment charset-validated and the SHA hex-checked in pathFor
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(s.diag, "prmap: unreadable cache entry (%v); refetching\n", err)
		}
		return nil, false
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.HeadSHA != k.HeadSHA {
		fmt.Fprintf(s.diag, "prmap: ignoring corrupt cache entry %s; refetching\n", filepath.Base(path))
		s.remove(path)
		return nil, false
	}
	return env.Files, true
}

// Put atomically writes env for k and prunes any sibling blob for the same PR
// (one entry per PR). Every failure is non-fatal and only noted on diag.
func (s *Store) Put(k Key, env Envelope) {
	path, ok := s.pathFor(k)
	if !ok {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(s.diag, "prmap: cannot create cache dir (%v); not caching\n", err)
		return
	}
	data, err := json.Marshal(env)
	if err != nil {
		fmt.Fprintf(s.diag, "prmap: cannot encode cache entry (%v); not caching\n", err)
		return
	}
	// Atomic publish: write a temp file in the destination dir (same filesystem
	// guarantees rename is atomic) then rename over the final name. A reader
	// therefore sees either no file or a complete one — never a torn write.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		fmt.Fprintf(s.diag, "prmap: cannot create cache temp (%v); not caching\n", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		s.remove(tmpName)
		fmt.Fprintf(s.diag, "prmap: cannot write cache temp (%v); not caching\n", err)
		return
	}
	if err := tmp.Close(); err != nil {
		s.remove(tmpName)
		fmt.Fprintf(s.diag, "prmap: cannot close cache temp (%v); not caching\n", err)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		s.remove(tmpName)
		fmt.Fprintf(s.diag, "prmap: cannot publish cache entry (%v); not caching\n", err)
		return
	}
	s.pruneSiblings(dir, filepath.Base(path))
}

// pathFor derives <root>/v1/<host>/<owner>/<repo>/pull/<N>/<headSHA>.json and
// verifies it stays under root. It returns ok=false (a no-op) for a no-root
// Store or a non-hex head SHA — the head SHA is the only segment not already
// charset-guaranteed by PRRef parsing.
func (s *Store) pathFor(k Key) (string, bool) {
	if s.root == "" {
		return "", false
	}
	if !hexSHARe.MatchString(k.HeadSHA) {
		fmt.Fprintf(s.diag, "prmap: refusing to cache a non-hex head SHA %q\n", k.HeadSHA)
		return "", false
	}
	host := k.Host
	if host == "" {
		host = "github.com"
	}
	p := filepath.Join(s.root, schemaVersion, host, k.Owner, k.Repo,
		"pull", fmt.Sprintf("%d", k.Number), k.HeadSHA+".json")
	// Defensive: every segment is charset-validated upstream, but confirm the
	// join did not escape root before any filesystem write.
	cleaned := filepath.Clean(p)
	rootClean := filepath.Clean(s.root)
	if cleaned != rootClean && !strings.HasPrefix(cleaned, rootClean+string(filepath.Separator)) {
		fmt.Fprintf(s.diag, "prmap: refusing a cache path outside root: %q\n", p)
		return "", false
	}
	return p, true
}

// pruneSiblings best-effort removes every *.json blob in dir except keep, plus
// any orphaned *.json.tmp-* temp (left by a process killed between CreateTemp
// and rename — its name never ends in .json, so nothing else would ever sweep
// it). This bounds the cache to one blob per PR and stops temps from
// accumulating forever. Prune races are benign — the worst case is one extra
// refetch, or a concurrent writer's rename failing (then it simply does not
// cache, which is already best-effort).
func (s *Store) pruneSiblings(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == keep {
			continue
		}
		if filepath.Ext(name) != ".json" && !strings.Contains(name, ".json.tmp-") {
			continue
		}
		s.remove(filepath.Join(dir, name))
	}
}

func (s *Store) remove(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(s.diag, "prmap: could not remove stale cache file (%v)\n", err)
	}
}
