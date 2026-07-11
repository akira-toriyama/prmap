// Package gh is the only place that knows the `gh` CLI surface. It shells to
// `gh api` (reusing gh's authentication, host configuration and pagination)
// through an injectable runner so the subprocess is a clean test seam. It holds
// no JSON API struct tags — all decoding lives in pure core — and it classifies
// every subprocess outcome into prmap's exit-code contract at the source.
package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/akira-toriyama/prmap/internal/core"
)

// maxStdout bounds captured gh output so an adversarial PR cannot OOM prmap.
// Real payloads stay well under this (the ~3000-file /files cap is ~6 MiB).
const maxStdout = 64 << 20

// errResponseTooLarge signals the stdout ceiling tripped; classify maps it.
var errResponseTooLarge = errors.New("gh response exceeded the size ceiling")

// Result is one subprocess outcome. Code is the process exit code (0 success,
// -1 = never ran). Err is set only for outcomes that are not an ordinary
// non-zero exit: gh missing, the size ceiling, or context cancellation. Keeping
// classification off exec's concrete error types makes it fully unit-testable.
type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
	Err    error
}

// RunFunc runs `gh` with args and returns the outcome. The default
// implementation shells out; tests inject a fake.
type RunFunc func(ctx context.Context, args ...string) Result

// Client fetches PR data via gh. The zero host uses gh's default / GH_HOST; a
// ref carrying an enterprise host routes to it via --hostname.
type Client struct {
	run RunFunc
}

// New returns a Client that shells to the real `gh` binary.
func New() *Client { return &Client{run: execRun} }

// Meta fetches the PR object and decodes it to core.Meta (head/base SHA and the
// authoritative counts).
func (c *Client) Meta(ctx context.Context, ref core.PRRef) (core.Meta, error) {
	res := c.run(ctx, c.apiArgs(ref, "")...)
	if res.Code != 0 || res.Err != nil {
		return core.Meta{}, classify(ctx, res)
	}
	return core.DecodeMeta(res.Stdout)
}

// Files fetches the full (paginated) /files array and returns its raw bytes
// verbatim — the caller caches these exact bytes so tier 2 re-reads them with
// no refetch. Shape validation is the caller's DecodeFiles.
func (c *Client) Files(ctx context.Context, ref core.PRRef) ([]byte, error) {
	res := c.run(ctx, c.apiArgs(ref, "/files", "--paginate")...)
	if res.Code != 0 || res.Err != nil {
		return nil, classify(ctx, res)
	}
	return res.Stdout, nil
}

// apiArgs builds the `gh api` argv. Path segments come from an already
// charset-validated PRRef, so there is no shell and no injection surface.
// --hostname is added only for a non-default enterprise host.
func (c *Client) apiArgs(ref core.PRRef, suffix string, extra ...string) []string {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d%s", ref.Owner, ref.Repo, ref.Number, suffix)
	args := append([]string{"api", path}, extra...)
	if ref.Host != "" && ref.Host != "github.com" {
		args = append(args, "--hostname", ref.Host)
	}
	return args
}

// execRun shells to `gh`, capturing stdout (bounded) and stderr into separate
// buffers so neither is inherited by the parent's streams. It uses
// exec.CommandContext so a cancelled context kills the child.
func execRun(ctx context.Context, args ...string) Result {
	// #nosec G204 -- "gh" is a fixed binary and args are built from a
	// charset-validated PRRef (no shell, no user-controlled program name).
	cmd := exec.CommandContext(ctx, "gh", args...)
	out := &capBuffer{limit: maxStdout}
	var errBuf bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &errBuf
	err := cmd.Run()

	res := Result{Stdout: out.buf.Bytes(), Stderr: errBuf.Bytes()}
	switch {
	case out.tripped:
		res.Err = errResponseTooLarge
		res.Code = -1
	case err == nil:
		res.Code = 0
	case ctx.Err() != nil:
		res.Err = ctx.Err()
		res.Code = -1
	case errors.Is(err, exec.ErrNotFound):
		res.Err = err
		res.Code = -1
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Code = ee.ExitCode()
		} else {
			res.Err = err
			res.Code = -1
		}
	}
	return res
}

// capBuffer accumulates up to limit bytes and then silently discards the rest,
// flagging tripped. It bounds memory (the OOM guard) without erroring the
// io.Copy that exec drives, so the child still drains cleanly.
type capBuffer struct {
	buf     bytes.Buffer
	limit   int
	tripped bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if c.tripped {
		return len(p), nil
	}
	if room := c.limit - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:room])
		c.tripped = true
		return len(p), nil
	}
	return c.buf.Write(p)
}
