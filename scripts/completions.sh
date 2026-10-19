#!/bin/sh
# Generates shell completions into completions/ for the release archives and
# the Homebrew cask. File names are the ones each shell looks up.
set -e
rm -rf completions
mkdir completions
go run . completion bash > completions/hunch
go run . completion zsh > completions/_hunch
go run . completion fish > completions/hunch.fish
