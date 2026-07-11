// Package cli is the cobra adapter — prmap's command-line presentation layer.
// It parses flags, calls the pure core and the app coordinator, and renders the
// result. It holds no domain logic. stdout carries pipeable payload only;
// diagnostics and error envelopes go to stderr.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/akira-toriyama/prmap/internal/app"
	"github.com/akira-toriyama/prmap/internal/cache"
	"github.com/akira-toriyama/prmap/internal/core"
	"github.com/akira-toriyama/prmap/internal/gh"
	"github.com/akira-toriyama/prmap/internal/version"
)

// out/errOut are the single funnel for process output: stdout = payload,
// stderr = diagnostics. No other file writes to os.Stdout/os.Stderr directly.
var (
	out    io.Writer = os.Stdout
	errOut io.Writer = os.Stderr
)

// newService builds the coordinator. It is a package var so tests can inject a
// fake Fetcher/Cache without touching the network or the real gh binary. diag
// carries verbose cache/transport diagnostics (io.Discard when quiet).
var newService = func(diag io.Writer) *app.Service {
	return &app.Service{Fetch: gh.New(), Cache: cache.Open(diag), Diag: diag}
}

// Execute builds the root command, runs it, and maps the result to the
// exit-code contract: 0 ok / 1 not-found|empty / 2 bad-usage|validation /
// 3+ internal|IO. On a non-zero exit it prints {"error":{...}} to stderr. It
// is the only place that decides the process exit code; main is just
// os.Exit(cli.Execute()).
//
// Signals: the root context cancels on the first SIGINT/SIGTERM so in-flight
// subprocess/API work can unwind gracefully; the deferred stop restores the
// default disposition, so a second Ctrl-C hard-kills a wedged process.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // restore default disposition: a second Ctrl-C terminates hard
	}()

	root := newRootCmd()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return int(core.CodeOK)
	}
	// Core/app code and the flag/arg validators all return *core.Error (flag
	// errors are wrapped as CodeValidation by SetFlagErrorFunc below). A bare
	// error that reaches here is therefore an unclassified failure — e.g. a
	// stdout write error surfaced by the JSON encoder — which is internal by
	// contract, matching core.ExitCode. It must NOT fall back to usage(2).
	ce := core.AsError(err)
	if ce == nil {
		ce = &core.Error{Code: core.CodeInternal, Msg: err.Error()}
	}
	renderError(ce)
	return int(ce.Code)
}

// options collects the parsed flags for one run.
type options struct {
	refresh    bool
	noCache    bool
	noCollapse bool
	verbose    bool
}

func newRootCmd() *cobra.Command {
	opts := &options{}
	root := &cobra.Command{
		Use:   "prmap <owner/repo#N>",
		Short: "Tiered PR diff reader: file map first, per-file patches within a byte budget",
		Long: "prmap reads a large pull-request diff in tiers so an AI coding agent never\n" +
			"has to pull the whole thing into context: first a compact file map (dirs,\n" +
			"files, +/- counts, generated-file collapse), then the patch of just the files\n" +
			"that matter, hard-capped by a byte budget.\n\n" +
			"  prmap owner/repo#123                                        # tier 1: file map (few KB)\n" +
			"  prmap https://github.com/owner/repo/pull/123                # a PR URL works too\n\n" +
			"Planned (not implemented yet — see docs/design.md):\n" +
			"  prmap owner/repo#123 app/src/split/central.c --budget 8000  # tier 2: one file's patch\n" +
			"  prmap owner/repo#123 --grep SYMBOL                          # which files/hunks touch SYMBOL\n\n" +
			"The /files response is cached on disk keyed by the PR head SHA, so the map and\n" +
			"every future per-file read come from one fetch. Override the cache root with\n" +
			"PRMAP_CACHE_DIR.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Resolve().String(),
		// Exactly one PR ref. A bare `prmap` prints help; a second positional is
		// the reserved tier-2 file path, which is not implemented yet.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return core.Validationf("bad-usage",
					"per-file patch (tier 2) is not implemented yet; pass only owner/repo#N")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runMap(cmd.Context(), args[0], opts)
		},
	}
	// A flag-parse failure is a usage error: wrap it as *core.Error so it exits
	// 2 with the structured envelope, and so the only bare error that can reach
	// Execute is a genuine internal failure.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return core.Validationf("bad-flag", "%v", err)
	})
	root.Flags().BoolVar(&opts.refresh, "refresh", false, "skip the cache read but write the fresh result back")
	root.Flags().BoolVar(&opts.noCache, "no-cache", false, "never read or write the disk cache (fetch fresh, discard)")
	root.Flags().BoolVar(&opts.noCollapse, "no-collapse", false, "list every file in the tree (disable generated-file collapse)")
	root.Flags().BoolVar(&opts.verbose, "verbose", false, "emit cache/transport diagnostics to stderr")
	root.SetOut(out)
	root.SetErr(errOut)
	return root
}

// renderError prints the structured error envelope to stderr — never stdout,
// so piping the payload into jq stays clean.
func renderError(e *core.Error) {
	env := map[string]any{"code": int(e.Code), "message": e.Msg}
	if e.ID != "" {
		env["id"] = e.ID
	}
	if e.Details != nil {
		env["details"] = e.Details
	}
	if err := writeJSON(errOut, map[string]any{"error": env}); err != nil {
		fmt.Fprintln(errOut, e.Msg)
	}
}

// writeJSON is the single JSON funnel for every payload and envelope: HTML
// escaping is off so <, >, & survive verbatim (messages echo diff/path/code
// content) and the emitted bytes match the on-disk encoding. Encode appends a
// trailing newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
