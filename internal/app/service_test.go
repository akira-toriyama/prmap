package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/akira-toriyama/prmap/internal/cache"
	"github.com/akira-toriyama/prmap/internal/core"
)

const headA = "116e2ea48f6ceb97f9bae5c2fa6b3ccaf12aac1c"
const headB = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeFetcher struct {
	meta       core.Meta
	files      []byte
	filesErr   error
	metaCalls  int
	filesCalls int
}

func (f *fakeFetcher) Meta(context.Context, core.PRRef) (core.Meta, error) {
	f.metaCalls++
	return f.meta, nil
}
func (f *fakeFetcher) Files(context.Context, core.PRRef) ([]byte, error) {
	f.filesCalls++
	return f.files, f.filesErr
}

type memCache struct {
	m    map[string]json.RawMessage
	puts int
}

func newMemCache() *memCache { return &memCache{m: map[string]json.RawMessage{}} }

// memKey mirrors the disk store's full path key (host/owner/repo/pull/N/head),
// not just the head SHA, so these tests cannot mask a cross-PR/cross-repo
// collision the real store would avoid.
func memKey(k cache.Key) string {
	return fmt.Sprintf("%s/%s/%s/%d/%s", k.Host, k.Owner, k.Repo, k.Number, k.HeadSHA)
}
func (c *memCache) Get(k cache.Key) (json.RawMessage, bool) {
	v, ok := c.m[memKey(k)]
	return v, ok
}
func (c *memCache) Put(k cache.Key, e cache.Envelope) {
	c.m[memKey(k)] = e.Files
	c.puts++
}

func filesJSON() []byte {
	return []byte(`[{"filename":"app/src/central.c","status":"modified","additions":210,"deletions":13,"patch":"@@ x"}]`)
}

func newService(f *fakeFetcher, c Cache) *Service {
	return &Service{Fetch: f, Cache: c}
}

func allOpts() Options { return Options{Collapse: true, ReadCache: true, WriteCache: true} }

func TestServiceColdFetchesAndCaches(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, ChangedFiles: 1, Additions: 210, Deletions: 13}, files: filesJSON()}
	c := newMemCache()
	m, err := newService(f, c).Map(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 1}, allOpts())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if f.filesCalls != 1 {
		t.Errorf("filesCalls = %d, want 1 (cold fetch)", f.filesCalls)
	}
	if c.puts != 1 {
		t.Errorf("cache puts = %d, want 1", c.puts)
	}
	if m.Files != 1 || m.Additions != 210 || m.Deletions != 13 {
		t.Errorf("map header = %+v, want files:1 +210 -13", m)
	}
}

func TestServiceWarmIsCacheHit(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, ChangedFiles: 1, Additions: 210, Deletions: 13}, files: filesJSON()}
	c := newMemCache()
	svc := newService(f, c)
	ref := core.PRRef{Owner: "o", Repo: "r", Number: 1}
	_, _ = svc.Map(context.Background(), ref, allOpts())
	_, err := svc.Map(context.Background(), ref, allOpts())
	if err != nil {
		t.Fatalf("second Map: %v", err)
	}
	if f.filesCalls != 1 {
		t.Errorf("filesCalls = %d, want 1 (second call served from cache)", f.filesCalls)
	}
	if f.metaCalls != 2 {
		t.Errorf("metaCalls = %d, want 2 (Meta probed every call to resolve head)", f.metaCalls)
	}
}

func TestServiceBaseShaChangeDoesNotRefetch(t *testing.T) {
	// prmap keys on head SHA only. A moved base branch (common on an active
	// main) must NOT bust the cache, or the cache is useless.
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, BaseSHA: "base1", ChangedFiles: 1, Additions: 210, Deletions: 13}, files: filesJSON()}
	c := newMemCache()
	svc := newService(f, c)
	ref := core.PRRef{Owner: "o", Repo: "r", Number: 1}
	_, _ = svc.Map(context.Background(), ref, allOpts())
	f.meta.BaseSHA = "base2-moved" // base advanced, head unchanged
	_, _ = svc.Map(context.Background(), ref, allOpts())
	if f.filesCalls != 1 {
		t.Errorf("filesCalls = %d, want 1 (base change must not refetch)", f.filesCalls)
	}
}

func TestServiceHeadShaChangeRefetches(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, ChangedFiles: 1, Additions: 210, Deletions: 13}, files: filesJSON()}
	c := newMemCache()
	svc := newService(f, c)
	ref := core.PRRef{Owner: "o", Repo: "r", Number: 1}
	_, _ = svc.Map(context.Background(), ref, allOpts())
	f.meta.HeadSHA = headB // a force-push
	_, _ = svc.Map(context.Background(), ref, allOpts())
	if f.filesCalls != 2 {
		t.Errorf("filesCalls = %d, want 2 (head change forces refetch)", f.filesCalls)
	}
}

func TestServiceRefreshSkipsReadButWrites(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, ChangedFiles: 1, Additions: 210, Deletions: 13}, files: filesJSON()}
	c := newMemCache()
	svc := newService(f, c)
	ref := core.PRRef{Owner: "o", Repo: "r", Number: 1}
	_, _ = svc.Map(context.Background(), ref, allOpts())
	_, _ = svc.Map(context.Background(), ref, Options{Collapse: true, ReadCache: false, WriteCache: true})
	if f.filesCalls != 2 {
		t.Errorf("filesCalls = %d, want 2 (--refresh skips the cache read)", f.filesCalls)
	}
	if c.puts != 2 {
		t.Errorf("cache puts = %d, want 2 (--refresh still writes)", c.puts)
	}
}

func TestServiceNoCacheNeitherReadsNorWrites(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, ChangedFiles: 1, Additions: 210, Deletions: 13}, files: filesJSON()}
	c := newMemCache()
	svc := newService(f, c)
	ref := core.PRRef{Owner: "o", Repo: "r", Number: 1}
	opts := Options{Collapse: true, ReadCache: false, WriteCache: false}
	_, _ = svc.Map(context.Background(), ref, opts)
	_, _ = svc.Map(context.Background(), ref, opts)
	if f.filesCalls != 2 {
		t.Errorf("filesCalls = %d, want 2 (--no-cache always fetches)", f.filesCalls)
	}
	if c.puts != 0 {
		t.Errorf("cache puts = %d, want 0 (--no-cache never writes)", c.puts)
	}
}

func TestServiceRejectsNonHexHead(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: "not-a-sha", ChangedFiles: 1}, files: filesJSON()}
	_, err := newService(f, newMemCache()).Map(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 1}, allOpts())
	if ce := core.AsError(err); ce == nil || ce.ID != "head-sha-invalid" || ce.Code != core.CodeInternal {
		t.Errorf("non-hex head = %v, want internal head-sha-invalid", err)
	}
}

func TestServiceBadFilesResponseNotCached(t *testing.T) {
	f := &fakeFetcher{meta: core.Meta{HeadSHA: headA, ChangedFiles: 1}, files: []byte(`{"message":"nope"}`)}
	c := newMemCache()
	_, err := newService(f, c).Map(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 1}, allOpts())
	if ce := core.AsError(err); ce == nil || ce.ID != "bad-response" {
		t.Errorf("bad files response = %v, want bad-response", err)
	}
	if c.puts != 0 {
		t.Errorf("cache puts = %d, want 0 (garbage must never be cached)", c.puts)
	}
}
