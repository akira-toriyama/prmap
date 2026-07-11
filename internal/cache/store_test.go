package cache

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const hexSHA = "116e2ea48f6ceb97f9bae5c2fa6b3ccaf12aac1c"

func testKey() Key {
	return Key{Owner: "zmkfirmware", Repo: "zmk", Number: 2477, HeadSHA: hexSHA}
}

func testEnvelope(files string) Envelope { return envFor(testKey(), files) }

// envFor builds an envelope whose head_sha matches its key (the invariant the
// Service upholds — both come from the same Meta probe).
func envFor(k Key, files string) Envelope {
	return Envelope{
		Schema: 1, Tool: "prmap", Host: "github.com",
		Owner: k.Owner, Repo: k.Repo, PR: k.Number,
		HeadSHA: k.HeadSHA, BaseSHA: "7e8c542c94908ac011ec7272a5f8ab10d2102632",
		Files: json.RawMessage(files),
	}
}

// jsonFiles walks root and returns every *.json path (cache blobs).
func jsonFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("PRMAP_CACHE_DIR", t.TempDir())
	s := Open(io.Discard)
	files := `[{"filename":"a.go","additions":1}]`
	s.Put(testKey(), testEnvelope(files))

	got, ok := s.Get(testKey())
	if !ok {
		t.Fatal("Get after Put = miss, want hit")
	}
	if !bytes.Equal(got, json.RawMessage(files)) {
		t.Errorf("Get = %s, want the exact stored files bytes %s", got, files)
	}
}

func TestCacheMiss(t *testing.T) {
	t.Setenv("PRMAP_CACHE_DIR", t.TempDir())
	s := Open(io.Discard)
	if _, ok := s.Get(testKey()); ok {
		t.Error("Get on an empty cache = hit, want miss")
	}
}

func TestCacheDifferentHeadIsMiss(t *testing.T) {
	t.Setenv("PRMAP_CACHE_DIR", t.TempDir())
	s := Open(io.Discard)
	s.Put(testKey(), testEnvelope(`[]`))
	other := testKey()
	other.HeadSHA = "0000000000000000000000000000000000000000"
	if _, ok := s.Get(other); ok {
		t.Error("Get with a different head SHA = hit, want miss (SHA is the key)")
	}
}

func TestCacheCorruptSelfHeals(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PRMAP_CACHE_DIR", root)
	s := Open(io.Discard)
	s.Put(testKey(), testEnvelope(`[{"filename":"a.go"}]`))

	blobs := jsonFiles(t, root)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 cache blob, found %d", len(blobs))
	}
	if err := os.WriteFile(blobs[0], []byte("}{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(testKey()); ok {
		t.Error("Get on a corrupt entry = hit, want miss")
	}
	if _, err := os.Stat(blobs[0]); !os.IsNotExist(err) {
		t.Error("corrupt cache entry was not removed on read")
	}
}

func TestCacheHeadSHAMismatchIsMiss(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PRMAP_CACHE_DIR", root)
	s := Open(io.Discard)
	s.Put(testKey(), testEnvelope(`[]`))
	blobs := jsonFiles(t, root)
	// Tamper the envelope's head_sha so it no longer matches its own filename.
	tampered := `{"schema":1,"head_sha":"deadbeef","files":[]}`
	if err := os.WriteFile(blobs[0], []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(testKey()); ok {
		t.Error("Get on a head-sha-mismatched entry = hit, want miss")
	}
}

func TestCachePrunesSiblings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PRMAP_CACHE_DIR", root)
	s := Open(io.Discard)
	s.Put(testKey(), testEnvelope(`[]`))
	// A new head SHA for the same PR should evict the old blob (one per PR).
	k2 := testKey()
	k2.HeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s.Put(k2, envFor(k2, `[{"filename":"b.go"}]`))

	blobs := jsonFiles(t, root)
	if len(blobs) != 1 {
		t.Fatalf("after a head change, expected 1 blob (pruned), found %d: %v", len(blobs), blobs)
	}
	if _, ok := s.Get(testKey()); ok {
		t.Error("old head SHA still hits after prune")
	}
	if _, ok := s.Get(k2); !ok {
		t.Error("new head SHA should hit")
	}
}

func TestCacheSweepsOrphanTemps(t *testing.T) {
	// A process killed between CreateTemp and rename leaves a *.json.tmp-* file;
	// a later Put must sweep it so orphans cannot accumulate forever.
	root := t.TempDir()
	t.Setenv("PRMAP_CACHE_DIR", root)
	s := Open(io.Discard)
	s.Put(testKey(), testEnvelope(`[]`))
	blobs := jsonFiles(t, root)
	dir := filepath.Dir(blobs[0])
	orphan := filepath.Join(dir, "116e2ea48f6ceb97f9bae5c2fa6b3ccaf12aac1c.json.tmp-999")
	if err := os.WriteFile(orphan, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A subsequent Put (any key in this PR dir) should prune the orphan.
	k2 := testKey()
	k2.HeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	s.Put(k2, envFor(k2, `[]`))
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphaned temp file was not swept by a later Put")
	}
}

func TestCacheConcurrent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PRMAP_CACHE_DIR", root)
	s := Open(io.Discard)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Put(testKey(), testEnvelope(`[{"filename":"a.go"}]`))
			if raw, ok := s.Get(testKey()); ok && !json.Valid(raw) {
				t.Errorf("concurrent Get returned invalid JSON: %s", raw)
			}
		}()
	}
	wg.Wait()
	// No torn temp files may survive.
	for _, p := range jsonFiles(t, root) {
		_ = p
	}
	var tmps []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(filepath.Base(p), ".tmp") {
			tmps = append(tmps, p)
		}
		return nil
	})
	if len(tmps) != 0 {
		t.Errorf("stray temp files after concurrent writes: %v", tmps)
	}
}

func TestCacheNoOpStore(t *testing.T) {
	// A Store with no resolvable root must degrade to a no-op, never crash.
	s := &Store{}
	s.Put(testKey(), testEnvelope(`[]`)) // must not panic
	if _, ok := s.Get(testKey()); ok {
		t.Error("no-op store Get = hit, want miss")
	}
}

func TestCacheNonHexHeadIsNoOp(t *testing.T) {
	// A non-hex head SHA must never become a filename; the op is skipped, not fatal.
	t.Setenv("PRMAP_CACHE_DIR", t.TempDir())
	s := Open(io.Discard)
	bad := Key{Owner: "o", Repo: "r", Number: 1, HeadSHA: "../etc/passwd"}
	s.Put(bad, testEnvelope(`[]`)) // must not write outside root / must not panic
	if _, ok := s.Get(bad); ok {
		t.Error("Get with a non-hex head = hit, want miss")
	}
}
