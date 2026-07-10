// prmap is a tiered pull-request diff reader for AI coding agents: a compact
// file map first, then per-file patches within a byte budget. main is the only
// untestable process boundary — everything below returns and is exercised by
// tests through cli.Execute.
package main

import (
	"os"

	"github.com/akira-toriyama/prmap/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
