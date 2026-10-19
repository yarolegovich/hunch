# Contributing

```sh
go test -race ./...
```

## Layout

| Path | |
| - | - |
| `internal/typesafe` | System One API client |
| `internal/provider` | providers (TypeSafe, OpenRouter, OpenCode Zen); adding a gateway that serves the System One API is one entry in its registry |
| `internal/cli` | commands, input/output, auth and config |
| `docs/` | command docs and the demo |

## Demo GIF

`docs/demo/run.sh` re-records `docs/demo/hunch-demo.gif`. It needs `asciinema`
and an API key (it calls the real API), and downloads `agg` on first use. The
commands are in `docs/demo/demo.sh`; colors are set at its top.

## Releases

Pushing a `v*` tag runs GoReleaser, which builds the binaries, creates the
GitHub Release and updates the Homebrew cask in
[yarolegovich/homebrew-tap](https://github.com/yarolegovich/homebrew-tap).
