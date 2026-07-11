package gh

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"

	"github.com/akira-toriyama/prmap/internal/core"
)

var (
	httpStatusRe = regexp.MustCompile(`\(HTTP (\d{3})\)`)
	rateLimitRe  = regexp.MustCompile(`(?i)rate limit|secondary rate`)
	networkRe    = regexp.MustCompile(`(?i)error connecting|dial |no such host|connection refused|i/o timeout|\bTLS\b|network|check your internet`)
)

// classify turns a gh subprocess outcome into prmap's exit-code contract. The
// ordering is deliberate: cancellation first (a killed gh may leave a
// misleading stderr), then non-HTTP process conditions, then the HTTP status
// carried in gh's stderr, then network heuristics, then a safe catch-all — so
// an unforeseen gh change degrades to a correct exit code, never a crash or a
// wrong soft/hard split.
func classify(ctx context.Context, res Result) *core.Error {
	if ctx.Err() != nil || errors.Is(res.Err, context.Canceled) || errors.Is(res.Err, context.DeadlineExceeded) {
		return core.Internalf("canceled", "prmap was interrupted before gh finished")
	}
	if errors.Is(res.Err, exec.ErrNotFound) {
		return core.Internalf("gh-missing",
			"the `gh` CLI is required but was not found on PATH — install it from https://cli.github.com")
	}
	if errors.Is(res.Err, errResponseTooLarge) {
		return core.Internalf("response-too-large",
			"the API response exceeded prmap's %d MiB safety ceiling", maxStdout>>20)
	}

	stderr := string(res.Stderr)

	// gh exits 4 on an authentication problem, sometimes before any HTTP round
	// trip, so check it ahead of the 401 token.
	if res.Code == 4 {
		return core.Internalf("gh-unauthenticated", "gh is not authenticated — run `gh auth login`")
	}

	if m := httpStatusRe.FindStringSubmatch(stderr); m != nil {
		return classifyHTTP(m[1], stderr)
	}
	if networkRe.MatchString(stderr) {
		return core.Internalf("network", "could not reach the GitHub API: %s", lastLine(stderr))
	}
	return core.Internalf("gh-failed", "gh failed (exit %d): %s", res.Code, lastLine(stderr))
}

func classifyHTTP(status, stderr string) *core.Error {
	switch status {
	case "401":
		return core.Internalf("gh-unauthenticated", "gh is not authenticated — run `gh auth login`")
	case "404":
		// GitHub returns 404 both for a missing PR and for a private one the
		// token cannot see; either way it is a soft miss, not a crash.
		return core.NotFoundf("pr-not-found", "pull request not found (or not visible): %s", lastLine(stderr))
	case "410":
		return core.NotFoundf("pr-gone", "pull request is gone (HTTP 410)")
	case "403":
		if rateLimitRe.MatchString(stderr) {
			return core.Internalf("rate-limited", "GitHub API rate limit hit — retry later: %s", lastLine(stderr))
		}
		return core.Internalf("forbidden", "forbidden (permission, SSO, or scope): %s", lastLine(stderr))
	case "429":
		return core.Internalf("rate-limited", "GitHub API rate limit hit — retry later: %s", lastLine(stderr))
	case "406":
		return core.Internalf("diff-too-large", "the API rejected the request as too large (HTTP 406)")
	case "422":
		return core.Internalf("unprocessable", "the API could not process the request (HTTP 422)")
	}
	if strings.HasPrefix(status, "5") {
		return core.Internalf("server-error", "GitHub server error (HTTP %s) — retry later", status)
	}
	e := core.Internalf("http-error", "unexpected API response (HTTP %s): %s", status, lastLine(stderr))
	e.Details = map[string]any{"status": status}
	return e
}

// lastLine returns the last non-empty line of s (gh's most diagnostic line),
// trimmed, or "" when there is none.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
