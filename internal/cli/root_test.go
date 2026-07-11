package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/prmap/internal/app"
	"github.com/akira-toriyama/prmap/internal/cache"
	"github.com/akira-toriyama/prmap/internal/core"
)

const testHeadSHA = "116e2ea48f6ceb97f9bae5c2fa6b3ccaf12aac1c"

// stubFetcher stands in for the gh client so the CLI is driven end-to-end with
// no network and no real gh binary.
type stubFetcher struct {
	meta     core.Meta
	files    []byte
	metaErr  error
	filesErr error
}

func (s stubFetcher) Meta(context.Context, core.PRRef) (core.Meta, error) {
	return s.meta, s.metaErr
}
func (s stubFetcher) Files(context.Context, core.PRRef) ([]byte, error) {
	return s.files, s.filesErr
}

// runExecute drives the real Execute() with args and a stubbed service,
// capturing stdout, stderr and the exit code. The cache is a hermetic temp dir.
func runExecute(t *testing.T, stub stubFetcher, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("PRMAP_CACHE_DIR", t.TempDir())
	var so, se bytes.Buffer
	oldOut, oldErr, oldNew, oldArgs := out, errOut, newService, os.Args
	out, errOut = &so, &se
	newService = func(diag io.Writer) *app.Service {
		return &app.Service{Fetch: stub, Cache: cache.Open(diag), Diag: diag}
	}
	os.Args = append([]string{"prmap"}, args...)
	t.Cleanup(func() { out, errOut, newService, os.Args = oldOut, oldErr, oldNew, oldArgs })
	code = Execute()
	return so.String(), se.String(), code
}

// The scaffold guarantees exactly this much: --help succeeds and renders the
// planned grammar. Real commands arrive with the implementation.
func TestRootHelp(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "prmap") {
		t.Errorf("help output does not mention the binary name: %q", got)
	}
	if !strings.Contains(got, "--budget") {
		t.Errorf("help output does not render the planned grammar sketch: %q", got)
	}
}

// The JSON funnel must not HTML-escape <, >, & — error messages (and the future
// payload) echo diff/path/code content, and stderr envelopes must byte-match the
// on-disk form so `jq` over them stays honest.
func TestRenderErrorNoHTMLEscape(t *testing.T) {
	old := errOut
	var buf bytes.Buffer
	errOut = &buf
	defer func() { errOut = old }()

	renderError(&core.Error{Code: core.CodeValidation, Msg: "line <html> & path a>b"})

	got := buf.String()
	// EscapeHTML(false) means the escaped \uXXXX forms must be absent and the raw
	// characters present verbatim.
	for _, escaped := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(got, escaped) {
			t.Errorf("error envelope contains an HTML-escaped sequence (%s): %s", escaped, got)
		}
	}
	if !strings.Contains(got, "line <html> & path a>b") {
		t.Errorf("message not emitted verbatim: %s", got)
	}
}

func okStub() stubFetcher {
	return stubFetcher{
		meta:  core.Meta{HeadSHA: testHeadSHA, ChangedFiles: 1, Additions: 210, Deletions: 13},
		files: []byte(`[{"filename":"app/src/central.c","status":"modified","additions":210,"deletions":13,"patch":"@@ x"}]`),
	}
}

// Tier 1 succeeds: the map goes to stdout, stderr stays clean, exit 0.
func TestTier1Success(t *testing.T) {
	so, se, code := runExecute(t, okStub(), "o/r#1")
	if code != int(core.CodeOK) {
		t.Errorf("exit = %d, want 0", code)
	}
	if se != "" {
		t.Errorf("stderr = %q, want empty", se)
	}
	for _, want := range []string{`"files":1`, `"+":210`, `"-":13`, `"central.c"`} {
		if !strings.Contains(so, want) {
			t.Errorf("stdout %q missing %q", so, want)
		}
	}
}

// A malformed ref is a usage error (exit 2) with the envelope on stderr and a
// clean stdout so a pipe is never fed junk.
func TestBadRefExits2(t *testing.T) {
	so, se, code := runExecute(t, okStub(), "not-a-ref")
	if code != int(core.CodeValidation) {
		t.Errorf("exit = %d, want 2", code)
	}
	if so != "" {
		t.Errorf("stdout = %q, want empty on error", so)
	}
	if !strings.Contains(se, `"id":"bad-ref"`) || !strings.Contains(se, `"code":2`) {
		t.Errorf("stderr envelope = %q, want a bad-ref/code-2 error", se)
	}
}

// A missing PR is a soft miss (exit 1), not a crash.
func TestNotFoundExits1(t *testing.T) {
	stub := okStub()
	stub.metaErr = core.NotFoundf("pr-not-found", "pull request not found")
	_, se, code := runExecute(t, stub, "o/r#404")
	if code != int(core.CodeNotFound) {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(se, `"id":"pr-not-found"`) {
		t.Errorf("stderr = %q, want a pr-not-found envelope", se)
	}
}

// A second positional (the reserved tier-2 path) is a usage error, not a crash.
func TestSecondPositionalExits2(t *testing.T) {
	_, se, code := runExecute(t, okStub(), "o/r#1", "app/src/central.c")
	if code != int(core.CodeValidation) {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(se, "tier 2") {
		t.Errorf("stderr = %q, want a tier-2-not-implemented message", se)
	}
}

// A bare invocation prints help and exits 0 (never a silent failure).
func TestBareInvocationHelps(t *testing.T) {
	_, _, code := runExecute(t, okStub())
	if code != int(core.CodeOK) {
		t.Errorf("bare prmap exit = %d, want 0", code)
	}
}

// An unknown flag is a usage error (exit 2), routed through FlagErrorFunc so it
// carries the structured envelope like every other error.
func TestUnknownFlagExits2(t *testing.T) {
	_, se, code := runExecute(t, okStub(), "o/r#1", "--nope")
	if code != int(core.CodeValidation) {
		t.Errorf("unknown flag exit = %d, want 2", code)
	}
	if !strings.Contains(se, `"code":2`) {
		t.Errorf("stderr = %q, want a code-2 envelope", se)
	}
}

// failWriter always errors, standing in for a full/broken stdout.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errWrite }

var errWrite = &writeErr{}

type writeErr struct{}

func (*writeErr) Error() string { return "simulated stdout write failure" }

// A stdout write failure is an IO/internal error (exit 3), NOT a usage error —
// the fallback must honor core.ExitCode's "unclassified = internal" contract.
func TestStdoutWriteFailureExits3(t *testing.T) {
	t.Setenv("PRMAP_CACHE_DIR", t.TempDir())
	var se bytes.Buffer
	oldOut, oldErr, oldNew, oldArgs := out, errOut, newService, os.Args
	out, errOut = failWriter{}, &se
	newService = func(diag io.Writer) *app.Service {
		return &app.Service{Fetch: okStub(), Cache: cache.Open(diag), Diag: diag}
	}
	os.Args = []string{"prmap", "o/r#1"}
	t.Cleanup(func() { out, errOut, newService, os.Args = oldOut, oldErr, oldNew, oldArgs })
	if code := Execute(); code != int(core.CodeInternal) {
		t.Errorf("stdout write failure exit = %d, want 3 (internal)", code)
	}
}
