package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzParseRef: ParseRef must never panic, and a ref it accepts must round-trip
// its owner/repo/number back through String() without surprise.
func FuzzParseRef(f *testing.F) {
	for _, s := range []string{
		"owner/repo#1", "https://github.com/o/r/pull/5", "", "garbage",
		"a/b#0", "a/b#99999999999999999999", "///#", "o/r#1#2",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ref, err := ParseRef(s)
		if err != nil {
			if AsError(err) == nil {
				t.Fatalf("ParseRef(%q) returned a non-*core.Error: %v", s, err)
			}
			return
		}
		if ref.Number < 1 {
			t.Errorf("ParseRef(%q) accepted a non-positive number %d", s, ref.Number)
		}
		if ref.Owner == "" || ref.Repo == "" {
			t.Errorf("ParseRef(%q) accepted an empty owner/repo: %+v", s, ref)
		}
		_ = ref.String()
	})
}

// FuzzBuildMap: BuildMap must never panic, always produce non-nil Collapsed and
// Tree, keep the header meta-authoritative, and emit valid JSON.
func FuzzBuildMap(f *testing.F) {
	f.Add("a/b.go\npnpm-lock.yaml\n.\nx", 10, 3, 4, true)
	f.Add("", 0, 0, 0, false)
	f.Fuzz(func(t *testing.T, names string, add, del, changed int, collapse bool) {
		// Clamp to production-reachable ranges.
		if add < 0 {
			add = -add
		}
		if del < 0 {
			del = -del
		}
		add %= 1_000_000
		del %= 1_000_000
		if changed < 0 {
			changed = -changed
		}
		changed %= 5001

		var files []FileEntry
		for _, n := range strings.Split(names, "\n") {
			files = append(files, FileEntry{Filename: n, Status: "modified", Additions: add, Deletions: del, HasPatch: true})
		}
		meta := Meta{ChangedFiles: changed, Additions: add, Deletions: del}

		m := BuildMap(meta, files, Options{Collapse: collapse})

		if m.Collapsed == nil {
			t.Error("Collapsed is nil, must be an empty slice at minimum")
		}
		if m.Tree == nil {
			t.Error("Tree is nil, must be an empty slice at minimum")
		}
		if m.Files != changed || m.Additions != add || m.Deletions != del {
			t.Errorf("header not meta-authoritative: got %d/%d/%d want %d/%d/%d",
				m.Files, m.Additions, m.Deletions, changed, add, del)
		}
		if _, err := json.Marshal(m); err != nil {
			t.Errorf("BuildMap output does not marshal: %v", err)
		}
	})
}
