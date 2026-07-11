package core

import (
	"os"
	"testing"
)

func TestDecodeMeta(t *testing.T) {
	raw, err := os.ReadFile("testdata/zmk-2477.pr.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	m, err := DecodeMeta(raw)
	if err != nil {
		t.Fatalf("DecodeMeta: %v", err)
	}
	want := Meta{
		HeadSHA:      "116e2ea48f6ceb97f9bae5c2fa6b3ccaf12aac1c",
		BaseSHA:      "7e8c542c94908ac011ec7272a5f8ab10d2102632",
		ChangedFiles: 119,
		Additions:    4226,
		Deletions:    232,
	}
	if m != want {
		t.Errorf("DecodeMeta = %+v, want %+v", m, want)
	}
}

func TestDecodeMetaBadResponse(t *testing.T) {
	// A JSON array where the PR object was expected must be a bad-response
	// internal error, never a panic or a zero-value Meta.
	_, err := DecodeMeta([]byte(`[1,2,3]`))
	if err == nil {
		t.Fatal("DecodeMeta over an array = nil error, want bad-response")
	}
	if ce := AsError(err); ce == nil || ce.ID != "bad-response" || ce.Code != CodeInternal {
		t.Errorf("DecodeMeta bad input error = %v, want internal bad-response", err)
	}
}

func TestDecodeFiles(t *testing.T) {
	raw, err := os.ReadFile("testdata/sample.files.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := DecodeFiles(raw)
	if err != nil {
		t.Fatalf("DecodeFiles: %v", err)
	}
	want := []FileEntry{
		{Filename: "app/src/split/central.c", Status: "modified", Additions: 210, Deletions: 13, HasPatch: true},
		{Filename: "app/src/pointing/input_listener.c", Status: "added", Additions: 373, Deletions: 0, HasPatch: true},
		{Filename: "app/include/zmk/pointing.h", PreviousFilename: "app/include/zmk/mouse.h", Status: "renamed", Additions: 2, Deletions: 2, HasPatch: true},
		{Filename: "app/tests/pointing/mkp/native_posix.keymap", Status: "modified", Additions: 8, Deletions: 1, HasPatch: false},
	}
	if len(got) != len(want) {
		t.Fatalf("DecodeFiles returned %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDecodeFilesBadResponse(t *testing.T) {
	// A JSON object where the /files array was expected -> bad-response.
	_, err := DecodeFiles([]byte(`{"message":"Not Found"}`))
	if err == nil {
		t.Fatal("DecodeFiles over an object = nil error, want bad-response")
	}
	if ce := AsError(err); ce == nil || ce.ID != "bad-response" || ce.Code != CodeInternal {
		t.Errorf("DecodeFiles bad input error = %v, want internal bad-response", err)
	}
}

func TestDecodeFilesExplicitNullPatch(t *testing.T) {
	// GitHub omits "patch" for patch-less files, but a defensive presence test
	// must also treat an explicit null (or empty string) as "no usable patch",
	// not as a present patch — otherwise the nopatch tier-2 hint is wrong.
	raw := []byte(`[
		{"filename":"a.bin","status":"modified","additions":1,"deletions":0,"patch":null},
		{"filename":"b.txt","status":"modified","additions":1,"deletions":0,"patch":""}
	]`)
	got, err := DecodeFiles(raw)
	if err != nil {
		t.Fatalf("DecodeFiles: %v", err)
	}
	for _, f := range got {
		if f.HasPatch {
			t.Errorf("%s: HasPatch=true for a null/empty patch, want false", f.Filename)
		}
	}
}

func TestDecodeFilesEmpty(t *testing.T) {
	// An empty PR (0 changed files) is a valid, normalized empty slice — not an
	// error, and never nil (callers index it unconditionally).
	got, err := DecodeFiles([]byte(`[]`))
	if err != nil {
		t.Fatalf("DecodeFiles([]) unexpected error: %v", err)
	}
	if got == nil {
		t.Error("DecodeFiles([]) returned a nil slice, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("DecodeFiles([]) len = %d, want 0", len(got))
	}
}
