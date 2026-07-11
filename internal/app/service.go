// Package app is prmap's coordinator: the single funnel through which a tier
// request flows. Service.Map resolves the PR head SHA (a cheap live probe),
// serves the /files bytes from the head-SHA cache when it can, fetches and
// caches them when it cannot, and hands the pure core builder the result. It
// depends on the Fetcher and Cache ports (satisfied by internal/gh and
// internal/cache) so it is testable with fakes and no network or disk. Tier 2
// will add Service.Patch on the same cached bytes.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/akira-toriyama/prmap/internal/cache"
	"github.com/akira-toriyama/prmap/internal/core"
)

// hexSHARe rejects a head SHA that is not hex before it is used as a cache-path
// segment or trusted at all — GitHub SHAs are always hex, so a non-hex value is
// a malformed response worth failing loudly on.
var hexSHARe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// Fetcher is the PR data source (internal/gh). Meta is the cheap head/base/count
// probe; Files returns the raw, paginated /files bytes.
type Fetcher interface {
	Meta(ctx context.Context, ref core.PRRef) (core.Meta, error)
	Files(ctx context.Context, ref core.PRRef) ([]byte, error)
}

// Cache is the head-SHA response cache (internal/cache). Get returns the cached
// /files bytes; Put stores an envelope. Both are best-effort — a nil/no-op
// cache is valid.
type Cache interface {
	Get(k cache.Key) (json.RawMessage, bool)
	Put(k cache.Key, env cache.Envelope)
}

// Service composes a Fetcher and a Cache. Diag receives verbose diagnostics
// (io.Discard when quiet).
type Service struct {
	Fetch Fetcher
	Cache Cache
	Diag  io.Writer
}

// Options controls one Map call: Collapse toggles the generated-file heuristic;
// ReadCache/WriteCache encode the --refresh / --no-cache policy.
type Options struct {
	Collapse   bool
	ReadCache  bool
	WriteCache bool
}

// Map returns the tier-1 file map for ref. It always makes the cheap Meta probe
// (so a moved head is detected and never served stale), then serves /files from
// the head-SHA cache on a hit or fetches-and-caches on a miss.
func (s *Service) Map(ctx context.Context, ref core.PRRef, opts Options) (core.Map, error) {
	meta, err := s.Fetch.Meta(ctx, ref)
	if err != nil {
		return core.Map{}, err
	}
	if !hexSHARe.MatchString(meta.HeadSHA) {
		return core.Map{}, core.Internalf("head-sha-invalid",
			"the API returned a non-hex head SHA %q — cannot proceed", meta.HeadSHA)
	}

	key := cache.Key{Host: ref.Host, Owner: ref.Owner, Repo: ref.Repo, Number: ref.Number, HeadSHA: meta.HeadSHA}

	files, err := s.filesFor(ctx, ref, meta, key, opts)
	if err != nil {
		return core.Map{}, err
	}
	return core.BuildMap(meta, files, core.Options{Collapse: opts.Collapse}), nil
}

// filesFor returns the decoded file entries for ref@head, from cache when
// allowed and valid, otherwise by fetching (and, when allowed, caching) the raw
// /files bytes. Garbage is validated (and rejected) before it is ever cached.
func (s *Service) filesFor(ctx context.Context, ref core.PRRef, meta core.Meta, key cache.Key, opts Options) ([]core.FileEntry, error) {
	if opts.ReadCache {
		if raw, ok := s.Cache.Get(key); ok {
			if files, err := core.DecodeFiles(raw); err == nil {
				s.logf("cache hit %s\n", meta.HeadSHA)
				return files, nil
			}
			s.logf("cache entry for %s did not decode; refetching\n", meta.HeadSHA)
		}
	}

	s.logf("fetching /files for %s@%s\n", ref, meta.HeadSHA)
	raw, err := s.Fetch.Files(ctx, ref)
	if err != nil {
		return nil, err
	}
	files, err := core.DecodeFiles(raw)
	if err != nil {
		return nil, err // bad-response — never cache it
	}
	if opts.WriteCache {
		s.Cache.Put(key, envelopeFor(ref, meta, raw))
	}
	return files, nil
}

func envelopeFor(ref core.PRRef, meta core.Meta, raw []byte) cache.Envelope {
	host := ref.Host
	if host == "" {
		host = "github.com"
	}
	return cache.Envelope{
		Schema:       1,
		Tool:         "prmap",
		Host:         host,
		Owner:        ref.Owner,
		Repo:         ref.Repo,
		PR:           ref.Number,
		HeadSHA:      meta.HeadSHA,
		BaseSHA:      meta.BaseSHA,
		ChangedFiles: meta.ChangedFiles,
		Additions:    meta.Additions,
		Deletions:    meta.Deletions,
		FetchedAt:    time.Now().UTC().Format(time.RFC3339),
		Files:        raw,
	}
}

func (s *Service) logf(format string, a ...any) {
	if s.Diag != nil {
		fmt.Fprintf(s.Diag, format, a...)
	}
}
