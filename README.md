# prmap

Tiered pull-request diff reader for AI coding agents: a compact **file map**
first, then **per-file patches within a byte budget** — so a 119-file PR can be
reviewed without pulling its 222 KB diff into context.

> **Status: tier 1 shipped.** The **file map** is implemented and cached; the
> per-file budget patch (tier 2), `--grep`, and hunk-level tier 3 are next.
> The design lives in [docs/design.md](docs/design.md).
>
> Requires the [`gh`](https://cli.github.com) CLI, authenticated (`gh auth
> login`) — prmap shells to `gh api`, reusing its credentials and host config.

## The pain this kills

- `gh pr diff` is all-or-nothing: 222 KB measured on a real 119-file PR.
- Diffs over ~20k lines make the API return **HTTP 406** — even
  `gh pr diff --name-only` dies exactly when structure matters most
  ([cli/cli#10712](https://github.com/cli/cli/issues/10712)).
- `gh pr view --json files` silently truncates at **100 files**
  ([cli/cli#5368](https://github.com/cli/cli/issues/5368)).
- Lockfiles and generated files carry most of the bytes and zero review signal.

## Tier 1: the file map (works today)

```console
$ prmap owner/repo#123
{"files":119,"+":4226,"-":232,"collapsed":["pnpm-lock.yaml (+3801)"],
 "tree":[{"dir":"app/src/split","files":[{"p":"central.c","+":210,"-":13}]}]}

$ prmap https://github.com/owner/repo/pull/123   # a PR URL works too
```

The top line is GitHub's own authoritative count (`files` / `+` / `-`). Each
tree node is `{"p":<basename>,"+":a,"-":d}`, plus `"s"` for a non-modified
status (`added`/`removed`/`renamed`/…), `"from"` for a rename's old path, and
`"nopatch":true` when GitHub omitted the patch (a tier-2 hint). Generated and
lock files are pulled out of the tree into `collapsed` (still counted in the
header). Output is deterministically sorted, so it is byte-stable across runs.

Responses are cached on disk keyed by the PR head SHA, so the map and every
future per-file read are served from one `/pulls/N/files` fetch instead of
re-downloading the full diff per call. A moved base branch does **not** bust the
cache; a new head commit (or force-push) does.

### Flags

| Flag | Effect |
|---|---|
| `--no-collapse` | list every file in the tree (disable generated-file collapse) |
| `--refresh` | skip the cache read, refetch, and rewrite the entry |
| `--no-cache` | never read or write the disk cache (fetch fresh, discard) |
| `--verbose` | emit cache/transport diagnostics to stderr |

`PRMAP_CACHE_DIR` (absolute) overrides the cache root
(default: `os.UserCacheDir()/prmap`).

## Planned (not implemented yet)

```console
$ prmap owner/repo#123 app/src/split/central.c --budget 8000   # tier 2
$ prmap owner/repo#123 --grep SYMBOL                           # symbol lookup
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | OK |
| `1` | not found / empty result |
| `2` | bad usage / validation — fix the args, do not retry |
| `3+` | internal / IO error |

stdout carries pipeable payload only; diagnostics and the JSON error envelope
(`{"error":{"code","message",…}}`) go to stderr.

## Install

From source, for now:

```sh
go install github.com/akira-toriyama/prmap/cmd/prmap@latest
```

## License

[MIT](LICENSE)
