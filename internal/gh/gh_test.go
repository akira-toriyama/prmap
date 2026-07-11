package gh

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/akira-toriyama/prmap/internal/core"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		res      Result
		wantCode core.Code
		wantID   string
	}{
		{"404 is a soft miss", Result{Stderr: []byte("gh: Not Found (HTTP 404)"), Code: 1}, core.CodeNotFound, "pr-not-found"},
		{"410 gone is a soft miss", Result{Stderr: []byte("gh: Gone (HTTP 410)"), Code: 1}, core.CodeNotFound, "pr-gone"},
		{"401 unauthenticated", Result{Stderr: []byte("gh: Bad credentials (HTTP 401)"), Code: 1}, core.CodeInternal, "gh-unauthenticated"},
		{"exit 4 unauthenticated", Result{Stderr: []byte("To get started with GitHub CLI, please run: gh auth login"), Code: 4}, core.CodeInternal, "gh-unauthenticated"},
		{"403 rate limit", Result{Stderr: []byte("gh: API rate limit exceeded (HTTP 403)"), Code: 1}, core.CodeInternal, "rate-limited"},
		{"403 secondary rate", Result{Stderr: []byte("You have exceeded a secondary rate limit (HTTP 403)"), Code: 1}, core.CodeInternal, "rate-limited"},
		{"403 plain forbidden", Result{Stderr: []byte("gh: Must have admin rights (HTTP 403)"), Code: 1}, core.CodeInternal, "forbidden"},
		{"429 rate limit", Result{Stderr: []byte("gh: Too Many Requests (HTTP 429)"), Code: 1}, core.CodeInternal, "rate-limited"},
		{"406 diff too large", Result{Stderr: []byte("gh: Not Acceptable (HTTP 406)"), Code: 1}, core.CodeInternal, "diff-too-large"},
		{"422 unprocessable", Result{Stderr: []byte("gh: Unprocessable Entity (HTTP 422)"), Code: 1}, core.CodeInternal, "unprocessable"},
		{"500 server error", Result{Stderr: []byte("gh: Internal Server Error (HTTP 500)"), Code: 1}, core.CodeInternal, "server-error"},
		{"unmatched http status", Result{Stderr: []byte("gh: I'm a teapot (HTTP 418)"), Code: 1}, core.CodeInternal, "http-error"},
		{"network failure", Result{Stderr: []byte("error connecting to api.github.com: no such host"), Code: 1}, core.CodeInternal, "network"},
		{"gh missing", Result{Err: exec.ErrNotFound, Code: -1}, core.CodeInternal, "gh-missing"},
		{"response too large", Result{Err: errResponseTooLarge, Code: -1}, core.CodeInternal, "response-too-large"},
		{"context canceled", Result{Err: context.Canceled, Code: -1}, core.CodeInternal, "canceled"},
		{"opaque non-zero exit", Result{Stderr: []byte("something odd happened"), Code: 7}, core.CodeInternal, "gh-failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(context.Background(), c.res)
			if got == nil {
				t.Fatal("classify returned nil, want a *core.Error")
			}
			if got.Code != c.wantCode {
				t.Errorf("code = %d, want %d", got.Code, c.wantCode)
			}
			if got.ID != c.wantID {
				t.Errorf("id = %q, want %q", got.ID, c.wantID)
			}
		})
	}
}

// canceledFirst: a killed gh subprocess whose stderr happens to contain an HTTP
// marker must still classify as canceled, never as a wrong HTTP class.
func TestClassifyCancellationWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := classify(ctx, Result{Stderr: []byte("gh: Not Found (HTTP 404)"), Code: 1})
	if got.ID != "canceled" {
		t.Errorf("id = %q, want canceled (cancellation checked first)", got.ID)
	}
}

type fakeRunner struct {
	res     Result
	gotArgs []string
}

func (f *fakeRunner) run(_ context.Context, args ...string) Result {
	f.gotArgs = args
	return f.res
}

func TestMetaDecodesAndBuildsArgv(t *testing.T) {
	pr := `{"head":{"sha":"abc123"},"base":{"sha":"def456"},"changed_files":3,"additions":10,"deletions":4}`
	fr := &fakeRunner{res: Result{Stdout: []byte(pr), Code: 0}}
	c := &Client{run: fr.run}

	meta, err := c.Meta(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 5})
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	want := core.Meta{HeadSHA: "abc123", BaseSHA: "def456", ChangedFiles: 3, Additions: 10, Deletions: 4}
	if meta != want {
		t.Errorf("Meta = %+v, want %+v", meta, want)
	}
	if !reflect.DeepEqual(fr.gotArgs, []string{"api", "repos/o/r/pulls/5"}) {
		t.Errorf("argv = %v, want [api repos/o/r/pulls/5]", fr.gotArgs)
	}
}

func TestFilesArgvPaginates(t *testing.T) {
	fr := &fakeRunner{res: Result{Stdout: []byte(`[{"filename":"a.go"}]`), Code: 0}}
	c := &Client{run: fr.run}
	raw, err := c.Files(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 5})
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if string(raw) != `[{"filename":"a.go"}]` {
		t.Errorf("Files returned %s, want the raw array verbatim", raw)
	}
	want := []string{"api", "repos/o/r/pulls/5/files", "--paginate"}
	if !reflect.DeepEqual(fr.gotArgs, want) {
		t.Errorf("argv = %v, want %v", fr.gotArgs, want)
	}
}

func TestEnterpriseHostAddsHostname(t *testing.T) {
	fr := &fakeRunner{res: Result{Stdout: []byte(`{"head":{"sha":"a"},"base":{"sha":"b"}}`), Code: 0}}
	c := &Client{run: fr.run}
	_, err := c.Meta(context.Background(), core.PRRef{Host: "ghe.corp", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	joined := ""
	for _, a := range fr.gotArgs {
		joined += a + " "
	}
	if want := "--hostname ghe.corp"; !contains(fr.gotArgs, "--hostname") || !contains(fr.gotArgs, "ghe.corp") {
		t.Errorf("argv %q missing %q for an enterprise host", joined, want)
	}
}

func TestDefaultHostOmitsHostname(t *testing.T) {
	fr := &fakeRunner{res: Result{Stdout: []byte(`{"head":{"sha":"a"},"base":{"sha":"b"}}`), Code: 0}}
	c := &Client{run: fr.run}
	_, _ = c.Meta(context.Background(), core.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 1})
	if contains(fr.gotArgs, "--hostname") {
		t.Errorf("argv %v should not carry --hostname for github.com", fr.gotArgs)
	}
}

func TestMetaClassifiesFailure(t *testing.T) {
	fr := &fakeRunner{res: Result{Stderr: []byte("gh: Not Found (HTTP 404)"), Code: 1}}
	c := &Client{run: fr.run}
	_, err := c.Meta(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 9})
	ce := core.AsError(err)
	if ce == nil || ce.ID != "pr-not-found" || ce.Code != core.CodeNotFound {
		t.Errorf("Meta on 404 = %v, want not-found pr-not-found", err)
	}
}

func TestMetaBadResponse(t *testing.T) {
	// gh exits 0 but hands back an array where the PR object was expected.
	fr := &fakeRunner{res: Result{Stdout: []byte(`[1,2,3]`), Code: 0}}
	c := &Client{run: fr.run}
	_, err := c.Meta(context.Background(), core.PRRef{Owner: "o", Repo: "r", Number: 1})
	if ce := core.AsError(err); ce == nil || ce.ID != "bad-response" {
		t.Errorf("Meta bad response = %v, want bad-response", err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
