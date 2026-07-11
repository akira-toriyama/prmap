package core

import (
	"bytes"
	"encoding/json"
)

// Meta is the pull-request-level summary read from the PR object. HeadSHA is
// the cache key; BaseSHA is retained for tier-2 (base/head blob diffing) and
// diagnostics but is deliberately NOT part of the cache key — keying on it too
// would force a refetch every time the base branch advances, defeating the
// cache on any PR against an active branch. The counts are meta-authoritative:
// GitHub's own totals, which stay correct even when /files is truncated at its
// ~3000-file cap.
type Meta struct {
	HeadSHA      string
	BaseSHA      string
	ChangedFiles int
	Additions    int
	Deletions    int
}

// FileEntry is one changed file, decoded from a /files array element down to
// just what the tier-1 map needs. HasPatch records whether the raw entry
// carried a "patch" field — GitHub omits it for large/binary files — so tier 1
// can flag it and tier 2 can skip a doomed read. The patch body itself is not
// retained here (tier 2 re-reads it from the cached raw bytes).
type FileEntry struct {
	Filename         string
	PreviousFilename string
	Status           string
	Additions        int
	Deletions        int
	HasPatch         bool
}

// DecodeMeta parses a GitHub PR object into Meta. A payload that is not a JSON
// object (e.g. an error array, or a gh output-format change) yields a
// bad-response internal error rather than a silent zero-value Meta.
func DecodeMeta(raw []byte) (Meta, error) {
	if !firstJSONToken(raw, '{') {
		return Meta{}, Internalf("bad-response", "expected a PR object from the API, got a non-object response")
	}
	var pr struct {
		Head         struct{ SHA string } `json:"head"`
		Base         struct{ SHA string } `json:"base"`
		ChangedFiles int                  `json:"changed_files"`
		Additions    int                  `json:"additions"`
		Deletions    int                  `json:"deletions"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return Meta{}, Internalf("bad-response", "malformed PR object from the API: %v", err)
	}
	return Meta{
		HeadSHA:      pr.Head.SHA,
		BaseSHA:      pr.Base.SHA,
		ChangedFiles: pr.ChangedFiles,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
	}, nil
}

// DecodeFiles parses a GitHub /files array into FileEntry values. The returned
// slice is never nil (an empty PR yields an empty non-nil slice so callers can
// index it unconditionally). A payload that is not a JSON array yields a
// bad-response internal error.
func DecodeFiles(raw []byte) ([]FileEntry, error) {
	if !firstJSONToken(raw, '[') {
		return nil, Internalf("bad-response", "expected a files array from the API, got a non-array response")
	}
	var wire []struct {
		Filename         string  `json:"filename"`
		PreviousFilename string  `json:"previous_filename"`
		Status           string  `json:"status"`
		Additions        int     `json:"additions"`
		Deletions        int     `json:"deletions"`
		Patch            *string `json:"patch"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, Internalf("bad-response", "malformed files array from the API: %v", err)
	}
	out := make([]FileEntry, len(wire))
	for i, w := range wire {
		out[i] = FileEntry{
			Filename:         w.Filename,
			PreviousFilename: w.PreviousFilename,
			Status:           w.Status,
			Additions:        w.Additions,
			Deletions:        w.Deletions,
			// A usable patch is present, non-null and non-empty. GitHub OMITS
			// the field for patch-less files (a nil pointer here); an explicit
			// null or "" is treated the same so the nopatch tier-2 hint stays
			// honest.
			HasPatch: w.Patch != nil && *w.Patch != "",
		}
	}
	return out, nil
}

// firstJSONToken reports whether the first non-whitespace byte of raw is want.
// It guards the decoders against a well-formed JSON value of the wrong shape
// (an object where an array is expected, or vice versa) so those degrade to a
// classified bad-response instead of an unmarshal into the wrong Go type.
func firstJSONToken(raw []byte, want byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == want
}
