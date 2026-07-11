package cli

import (
	"context"
	"io"

	"github.com/akira-toriyama/prmap/internal/app"
	"github.com/akira-toriyama/prmap/internal/core"
)

// runMap executes tier 1: parse the ref, build the coordinator, fetch/serve the
// map, and write it to stdout. Every error is a *core.Error and flows back up
// to Execute for the exit-code contract.
func runMap(ctx context.Context, ref string, opts *options) error {
	pr, err := core.ParseRef(ref)
	if err != nil {
		return err
	}

	diag := io.Discard
	if opts.verbose {
		diag = errOut
	}
	svc := newService(diag)

	m, err := svc.Map(ctx, pr, app.Options{
		Collapse:   !opts.noCollapse,
		ReadCache:  !opts.refresh && !opts.noCache,
		WriteCache: !opts.noCache,
	})
	if err != nil {
		return err
	}
	return writeJSON(out, m)
}
