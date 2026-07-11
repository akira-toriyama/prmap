# prmap — design

> Distilled from tracker task `t-ge4w` (private board, 2026-07-03 research,
> refined 2026-07-06). This is the pre-implementation design; verify premises
> against the linked upstream issues before building on them.

> **Status (2026-07-11): tier 1 shipped.** The file map, the head-SHA disk
> cache, the `gh api` transport, and the full fetch-error → exit-code mapping
> are implemented (`internal/{core,gh,cache,app,cli}`). Cache keying is **head
> SHA only** — a deliberate departure from the "head+base" idea below, because
> keying on base too would refetch on every base-branch advance and defeat the
> cache on any PR against an active branch. Next: tier 2 (per-file budget patch,
> hunk-granular) reading the same cached `/files` bytes, then `--grep` and
> hunk-level tier 3.

## What

A tiered PR diff reader: tier 1 is a compact file map (a few KB — dirs, files,
+/- counts, generated files collapsed); tier 2 is the patch of one requested
file, hard-capped by a byte budget. Target user is an AI coding agent that
must review large PRs inside a bounded context window.

## Pain (measured)

- `gh pr diff` is all-or-nothing: **222 KB** measured on zmkfirmware/zmk#2477
  (119 files).
- Diffs > ~20k lines: the API returns **HTTP 406** and `gh pr diff` dies even
  with `--name-only` (cli/cli#10712, open) — total dead end exactly on the PRs
  where structure is most needed.
- The `/pulls/N/files` fallback pages at 30/page and embeds the raw patch in
  every entry — "just the file list" still downloads the whole diff.
- `gh pr view --json files` silently truncates at **100 files** (cli/cli#5368,
  #9916).
- Lockfiles / generated files (pnpm-lock etc.) carry most bytes, zero signal.

## Honest note (verified)

The two-tier flow itself is reproducible today with a
`gh api /pulls/N/files --paginate --jq` one-liner (file map ≈ 7 KB; per-file
patch is also one turn; `/files` does not 406 — verified). **The binary's
justification is the cache**: fetch `/files` once keyed by head SHA, store on
disk, and serve both the map and every per-file read from it — the jq recipe
re-downloads 222 KB per call and burns rate limit.

## Design notes (from the verification agent's refinement)

1. Distribution: `gh prmap` (gh extension) **plus a bundled agent skill** —
   without the skill, agents keep reflexively running `gh pr diff` and never
   discover the tool. *(Repo-side note, not from the task body: open question —
   standalone binary + brew tap, the house default, vs gh extension; the repo
   is named `prmap` either way and a `gh-prmap` shim can come later.)*
2. Budget truncation at **hunk granularity** — head+tail cutting destroys the
   mid-file hunks that are usually the review target.
3. When the API silently omits the `patch` field (giant files), fill it by
   fetching base/head blobs and diffing locally — no existing tool does this.
4. Two genuinely novel primitives: `--grep SYMBOL` (which files/hunks touch
   this symbol across the whole PR — extremely agent-shaped) and hunk-level
   tier 3 (`--hunk 2`).
5. v1 de-scope: filename heuristics catch ~95% of lockfiles (skip
   .gitattributes linguist-generated fetch and the local-clone fallback).
   406 / too-large / patch-omitted get distinct exit-code treatment.

## Prior art

- `gh api --jq` one-liners: no cache, schema knowledge required, rate-limit burn.
- github-mcp-server: unbounded responses crash clients (issue #2122).

## Refs

- https://github.com/cli/cli/issues/10712 (406 on >20k-line diffs)
- https://github.com/cli/cli/issues/5368 (--json files 100-file cap)
- https://github.com/github/github-mcp-server/issues/2122 (unbounded MCP responses)
