# prmap

Tiered pull-request diff reader for AI coding agents: a compact **file map**
first, then **per-file patches within a byte budget** — so a 119-file PR can be
reviewed without pulling its 222 KB diff into context.

> **Status: pre-v0 scaffold.** The house Go CLI spine (exit-code contract,
> stdout/stderr discipline, cobra shell) is in place; the diff logic is not
> implemented yet. The design lives in [docs/design.md](docs/design.md).

## The pain this kills

- `gh pr diff` is all-or-nothing: 222 KB measured on a real 119-file PR.
- Diffs over ~20k lines make the API return **HTTP 406** — even
  `gh pr diff --name-only` dies exactly when structure matters most
  ([cli/cli#10712](https://github.com/cli/cli/issues/10712)).
- `gh pr view --json files` silently truncates at **100 files**
  ([cli/cli#5368](https://github.com/cli/cli/issues/5368)).
- Lockfiles and generated files carry most of the bytes and zero review signal.

## Planned CLI

```console
$ prmap owner/repo#123
{"files":119,"+":4226,"-":232,"collapsed":["pnpm-lock.yaml (+3801)"],
 "tree":[{"dir":"app/src/split","files":[{"p":"central.c","+":210,"-":13}]}]}

$ prmap owner/repo#123 app/src/split/central.c --budget 8000
$ prmap owner/repo#123 --grep SYMBOL
```

Responses are cached on disk keyed by the PR head SHA, so the map and every
per-file read are served from one `/pulls/N/files` fetch instead of
re-downloading the full diff per call.

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
