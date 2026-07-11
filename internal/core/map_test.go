package core

import (
	"bytes"
	"encoding/json"
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "regenerate testdata/*.golden.json")

// renderJSON mirrors the cli payload funnel (SetEscapeHTML(false), compact,
// trailing newline) so golden bytes match what the binary writes to stdout.
func renderJSON(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// The canonical multi-directory input: a collapsed lockfile, a cross-dir
// rename, an added file, a root file, and a patch-omitted file.
func sampleMeta() Meta {
	return Meta{HeadSHA: "abc123", BaseSHA: "def456", ChangedFiles: 6, Additions: 3801 + 2 + 373 + 210 + 2 + 8, Deletions: 0 + 2 + 0 + 13 + 1}
}

func sampleFiles() []FileEntry {
	return []FileEntry{
		{Filename: "pnpm-lock.yaml", Status: "modified", Additions: 3801, Deletions: 0, HasPatch: true},
		{Filename: "app/include/zmk/pointing.h", PreviousFilename: "app/include/zmk/mouse.h", Status: "renamed", Additions: 2, Deletions: 2, HasPatch: true},
		{Filename: "app/src/pointing/input_listener.c", Status: "added", Additions: 373, Deletions: 0, HasPatch: true},
		{Filename: "app/src/split/central.c", Status: "modified", Additions: 210, Deletions: 13, HasPatch: true},
		{Filename: "flake.nix", Status: "modified", Additions: 2, Deletions: 1, HasPatch: true},
		{Filename: "app/tests/mkp/native_posix.keymap", Status: "modified", Additions: 8, Deletions: 0, HasPatch: false},
	}
}

func TestBuildMapHeaderIsMetaAuthoritative(t *testing.T) {
	m := BuildMap(Meta{ChangedFiles: 119, Additions: 4226, Deletions: 232}, sampleFiles(), Options{Collapse: true})
	if m.Files != 119 || m.Additions != 4226 || m.Deletions != 232 {
		t.Errorf("header = files:%d +%d -%d, want 119 +4226 -232 (from meta, not summed)", m.Files, m.Additions, m.Deletions)
	}
}

func TestBuildMapCollapse(t *testing.T) {
	m := BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: true})
	// pnpm-lock.yaml (generated, deletions 0) collapses to the D==0 form with
	// the FULL path; it is NOT in the tree.
	if len(m.Collapsed) != 1 || m.Collapsed[0] != "pnpm-lock.yaml (+3801)" {
		t.Errorf("Collapsed = %v, want [\"pnpm-lock.yaml (+3801)\"]", m.Collapsed)
	}
	for _, g := range m.Tree {
		for _, f := range g.Files {
			if f.Path == "pnpm-lock.yaml" {
				t.Error("collapsed file pnpm-lock.yaml still appears in the tree")
			}
		}
	}
	// Collapsed files stay counted in the header (they are in meta).
	if m.Files != sampleMeta().ChangedFiles {
		t.Errorf("Files = %d, want %d (collapsed still counted)", m.Files, sampleMeta().ChangedFiles)
	}
}

func TestBuildMapNoCollapse(t *testing.T) {
	m := BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: false})
	if len(m.Collapsed) != 0 {
		t.Errorf("Collapsed = %v, want empty with --no-collapse", m.Collapsed)
	}
	if m.Collapsed == nil {
		t.Error("Collapsed is nil, want an empty non-nil slice")
	}
	// pnpm-lock.yaml now appears in the tree at the root.
	found := false
	for _, g := range m.Tree {
		if g.Dir == "." {
			for _, f := range g.Files {
				if f.Path == "pnpm-lock.yaml" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("with --no-collapse, pnpm-lock.yaml should be in tree under dir \".\"")
	}
}

func TestBuildMapRenamePlacement(t *testing.T) {
	m := BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: true})
	var node *FileNode
	for i := range m.Tree {
		if m.Tree[i].Dir == "app/include/zmk" {
			for j := range m.Tree[i].Files {
				if m.Tree[i].Files[j].Path == "pointing.h" {
					node = &m.Tree[i].Files[j]
				}
			}
		}
	}
	if node == nil {
		t.Fatal("renamed file not found under its NEW directory app/include/zmk as pointing.h")
	}
	if node.Status != "renamed" {
		t.Errorf("rename status = %q, want %q", node.Status, "renamed")
	}
	if node.From != "app/include/zmk/mouse.h" {
		t.Errorf("rename from = %q, want the FULL old path", node.From)
	}
}

func TestBuildMapStatusOnlyForNonModified(t *testing.T) {
	m := BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: true})
	for _, g := range m.Tree {
		for _, f := range g.Files {
			if f.Path == "central.c" && f.Status != "" {
				t.Errorf("modified file central.c has s=%q, want empty (omitted)", f.Status)
			}
			if f.Path == "input_listener.c" && f.Status != "added" {
				t.Errorf("added file input_listener.c has s=%q, want \"added\"", f.Status)
			}
		}
	}
}

func TestBuildMapNoPatchFlag(t *testing.T) {
	m := BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: true})
	for _, g := range m.Tree {
		for _, f := range g.Files {
			if f.Path == "native_posix.keymap" && !f.NoPatch {
				t.Error("patch-omitted file should have nopatch=true")
			}
			if f.Path == "central.c" && f.NoPatch {
				t.Error("file with a patch must not have nopatch set")
			}
		}
	}
}

func TestBuildMapTruncated(t *testing.T) {
	// Fewer /files entries than meta.changed_files => the ~3000-file cap => truncated.
	m := BuildMap(Meta{ChangedFiles: 5000, Additions: 1, Deletions: 0}, sampleFiles(), Options{Collapse: true})
	if !m.Truncated {
		t.Error("Truncated should be true when len(files) < meta.ChangedFiles")
	}
	// And omitted (not truncated) on a complete fetch.
	full := BuildMap(Meta{ChangedFiles: 6}, sampleFiles(), Options{Collapse: true})
	if full.Truncated {
		t.Error("Truncated should be false when all files are present")
	}
}

func TestBuildMapEmptyPR(t *testing.T) {
	m := BuildMap(Meta{ChangedFiles: 0}, nil, Options{Collapse: true})
	if m.Files != 0 || m.Additions != 0 || m.Deletions != 0 {
		t.Errorf("empty PR header = %+v, want all zero", m)
	}
	if m.Collapsed == nil || len(m.Collapsed) != 0 {
		t.Errorf("empty PR Collapsed = %v, want [] (non-nil)", m.Collapsed)
	}
	if m.Tree == nil || len(m.Tree) != 0 {
		t.Errorf("empty PR Tree = %v, want [] (non-nil)", m.Tree)
	}
	// It must serialize as [] not null.
	got := renderJSON(t, m)
	if !bytes.Contains(got, []byte(`"collapsed":[]`)) || !bytes.Contains(got, []byte(`"tree":[]`)) {
		t.Errorf("empty PR JSON = %s, want []-normalized collapsed/tree", got)
	}
}

func TestBuildMapDeterministicUnderShuffle(t *testing.T) {
	base := renderJSON(t, BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: true}))
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		fs := sampleFiles()
		r.Shuffle(len(fs), func(a, b int) { fs[a], fs[b] = fs[b], fs[a] })
		got := renderJSON(t, BuildMap(sampleMeta(), fs, Options{Collapse: true}))
		if !bytes.Equal(base, got) {
			t.Fatalf("output not deterministic under input shuffle:\n base=%s\n got =%s", base, got)
		}
	}
}

func TestBuildMapInvariantHeaderEqualsSum(t *testing.T) {
	m := BuildMap(sampleMeta(), sampleFiles(), Options{Collapse: true})
	if m.Truncated {
		t.Skip("invariant only holds on a complete fetch")
	}
	sumA, sumD, count := 0, 0, 0
	for _, g := range m.Tree {
		for _, f := range g.Files {
			sumA += f.Additions
			sumD += f.Deletions
			count++
		}
	}
	// collapsed adds are folded back via the label; re-derive from the input.
	for _, f := range sampleFiles() {
		if IsGenerated(f.Filename) {
			sumA += f.Additions
			sumD += f.Deletions
			count++
		}
	}
	if sumA != m.Additions || sumD != m.Deletions {
		t.Errorf("sum(+/-)=%d/%d != header %d/%d", sumA, sumD, m.Additions, m.Deletions)
	}
	if count != m.Files {
		t.Errorf("tree+collapsed count = %d != header files %d", count, m.Files)
	}
}

func TestBuildMapGolden(t *testing.T) {
	cases := []struct {
		name string
		meta Meta
		f    []FileEntry
		opts Options
	}{
		{"collapse", sampleMeta(), sampleFiles(), Options{Collapse: true}},
		{"no-collapse", sampleMeta(), sampleFiles(), Options{Collapse: false}},
		{"empty", Meta{ChangedFiles: 0}, nil, Options{Collapse: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderJSON(t, BuildMap(c.meta, c.f, c.opts))
			golden := filepath.Join("testdata", "map-"+c.name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run -update): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("golden mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
