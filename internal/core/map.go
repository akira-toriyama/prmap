package core

import (
	"path"
	"sort"
	"strconv"
)

// Map is the tier-1 file map: the whole compact JSON payload. Struct field
// order IS JSON key order (encoding/json preserves it) and the keys are short
// on purpose — this is agent-facing, and the point is to fit a large PR's
// structure into a bounded context window.
//
// Header totals (Files/Additions/Deletions) are meta-authoritative — GitHub's
// own PR counts — so they stay correct even when /files is truncated at its
// ~3000-file cap and even for files collapsed out of the tree. Collapsed and
// Tree are always non-nil so an agent can index them unconditionally.
type Map struct {
	Files     int        `json:"files"`
	Additions int        `json:"+"`
	Deletions int        `json:"-,"` //nolint:staticcheck // "-," emits the literal "-" key; plain "-" skips the field and the quoted form staticcheck suggests silently falls back to the Go field name (both verified)
	Truncated bool       `json:"truncated,omitempty"`
	Collapsed []string   `json:"collapsed"`
	Tree      []DirGroup `json:"tree"`
}

// DirGroup is one directory's changed files. Dir is the full '/'-path; the
// repository root is ".". Files is non-empty by construction.
type DirGroup struct {
	Dir   string     `json:"dir"`
	Files []FileNode `json:"files"`
}

// FileNode is one changed file within a directory group. Path is the basename
// (the group carries the directory). Status is surfaced only when it is not a
// plain modification; From carries the full old path of a rename/copy; NoPatch
// flags a file whose patch GitHub omitted (a tier-2 hint).
type FileNode struct {
	Path      string `json:"p"`
	Additions int    `json:"+"`
	Deletions int    `json:"-,"` //nolint:staticcheck // see Map.Deletions: "-," emits the "-" key; plain "-" would skip the field
	Status    string `json:"s,omitempty"`
	From      string `json:"from,omitempty"`
	NoPatch   bool   `json:"nopatch,omitempty"`
}

// Options controls tier-1 rendering. Collapse toggles the generated-file
// heuristic (default on; --no-collapse turns it off).
type Options struct {
	Collapse bool
}

// BuildMap assembles the tier-1 map from the PR meta and its file entries. It
// is pure and deterministic: the output is sorted byte-wise (directories, files
// within a directory, and the collapsed list) so it is byte-identical across
// runs regardless of the order the API returned the files in.
func BuildMap(meta Meta, files []FileEntry, opts Options) Map {
	type collapsedItem struct {
		path      string
		additions int
		deletions int
	}
	var collapsed []collapsedItem
	byDir := map[string][]FileNode{}

	for _, f := range files {
		if opts.Collapse && IsGenerated(f.Filename) {
			collapsed = append(collapsed, collapsedItem{f.Filename, f.Additions, f.Deletions})
			continue
		}
		node := FileNode{
			Path:      path.Base(f.Filename),
			Additions: f.Additions,
			Deletions: f.Deletions,
			NoPatch:   !f.HasPatch,
		}
		if f.Status != "" && f.Status != "modified" {
			node.Status = f.Status
		}
		if f.PreviousFilename != "" {
			node.From = f.PreviousFilename
		}
		dir := path.Dir(f.Filename)
		byDir[dir] = append(byDir[dir], node)
	}

	// Deterministic collapsed list, sorted by full path, then formatted.
	sort.Slice(collapsed, func(i, j int) bool { return collapsed[i].path < collapsed[j].path })
	collapsedLabels := make([]string, len(collapsed))
	for i, c := range collapsed {
		collapsedLabels[i] = c.path + " " + churn(c.additions, c.deletions)
	}

	// Deterministic tree, sorted by dir then by file basename.
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	tree := make([]DirGroup, 0, len(dirs))
	for _, d := range dirs {
		nodes := byDir[d]
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
		tree = append(tree, DirGroup{Dir: d, Files: nodes})
	}

	return Map{
		Files:     meta.ChangedFiles,
		Additions: meta.Additions,
		Deletions: meta.Deletions,
		Truncated: len(files) < meta.ChangedFiles,
		Collapsed: collapsedLabels,
		Tree:      tree,
	}
}

// churn formats a +/- summary in parentheses for a collapsed file's label:
// "(+A/-D)", "(+A)" (no deletions, incl. both-zero -> "(+0)"), or "(-D)"
// (deletions only).
func churn(add, del int) string {
	switch {
	case add > 0 && del > 0:
		return "(+" + strconv.Itoa(add) + "/-" + strconv.Itoa(del) + ")"
	case del == 0:
		return "(+" + strconv.Itoa(add) + ")"
	default:
		return "(-" + strconv.Itoa(del) + ")"
	}
}
