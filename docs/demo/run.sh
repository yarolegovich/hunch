#!/usr/bin/env bash
#
# Re-create the README demo GIF (docs/demo/hunch-demo.gif).
#
# Builds hunch from this checkout, records demo.sh with asciinema, and renders
# the cast to a GIF with agg. The demo calls the real TypeSafe API, so set
# TYPESAFE_API_KEY (or run `hunch auth login`) first. Run it from anywhere:
#
#   docs/demo/run.sh
#
# It needs `go` and `asciinema` on PATH. It downloads `agg` into a cache dir
# the first time, so no other install is required.
#   asciinema: https://docs.asciinema.org/manual/cli/installation/
#
# Tunables (env vars, with defaults):
#   THEME  github-dark               agg color theme (see `agg --help`)
#   FONT   agg's default list        text font family, e.g. "JetBrains Mono"
#   COLS   112                       terminal columns
#   ROWS   28                        terminal rows
#   HUNCH_DEMO_CACHE   ~/.cache/hunch-demo   where agg is cached

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DEMO_DIR="$ROOT/docs/demo"
CACHE="${HUNCH_DEMO_CACHE:-$HOME/.cache/hunch-demo}"

THEME="${THEME:-github-dark}"
COLS="${COLS:-112}"
ROWS="${ROWS:-28}"
GIF="${GIF:-$DEMO_DIR/hunch-demo.gif}"

mkdir -p "$CACHE/bin"

for tool in go asciinema; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: '$tool' not found on PATH" >&2; exit 1; }
done

# --- bootstrap agg (prebuilt binary) if missing -----------------------------
if ! command -v agg >/dev/null 2>&1; then
  if [[ ! -x "$CACHE/bin/agg" ]]; then
    case "$(uname -s)/$(uname -m)" in
      Linux/x86_64)  asset="agg-x86_64-unknown-linux-gnu" ;;
      Linux/aarch64) asset="agg-aarch64-unknown-linux-gnu" ;;
      Darwin/x86_64) asset="agg-x86_64-apple-darwin" ;;
      Darwin/arm64)  asset="agg-aarch64-apple-darwin" ;;
      *) echo "error: no prebuilt agg for $(uname -s)/$(uname -m); install agg manually" >&2; exit 1 ;;
    esac
    echo "==> fetching agg ($asset)"
    curl -fsSL -o "$CACHE/bin/agg" \
      "https://github.com/asciinema/agg/releases/latest/download/$asset"
    chmod +x "$CACHE/bin/agg"
  fi
  export PATH="$CACHE/bin:$PATH"
fi

# agg falls back through its own list of common monospace fonts unless FONT
# names one. The demo uses no special glyphs, so any monospace font works.
font_args=()
if [[ -n "${FONT:-}" ]]; then
  font_args=(--text-font-family "$FONT")
fi

# --- build hunch from this checkout -----------------------------------------
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR"' EXIT
VERSION="$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo dev)"
echo "==> building hunch ($VERSION)"
( cd "$ROOT" && go build -ldflags "-X github.com/yarolegovich/hunch/internal/cli.ver=$VERSION" -o "$BIN_DIR/hunch" . )
export PATH="$BIN_DIR:$PATH"

# --- the demo talks to the real API -----------------------------------------
if ! hunch auth status >/dev/null 2>&1; then
  echo "error: no TypeSafe API key; set TYPESAFE_API_KEY or run 'hunch auth login'" >&2
  exit 1
fi

CAST="$BIN_DIR/hunch-demo.cast"

echo "==> recording demo.sh (${COLS}x${ROWS})"
asciinema rec --cols "$COLS" --rows "$ROWS" --overwrite \
  --title "hunch — typed judgments for your pipelines" \
  --command "bash $DEMO_DIR/demo.sh" \
  "$CAST"

echo "==> rendering GIF (theme=$THEME)"
agg --theme "$THEME" --font-size 16 --line-height 1.35 \
  --idle-time-limit 2.5 ${font_args[@]+"${font_args[@]}"} \
  "$CAST" "$GIF"

echo "==> done: $GIF"
