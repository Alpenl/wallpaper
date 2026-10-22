#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# go may select a newer toolchain than the standalone gofmt on PATH.
gofmt="$(go env GOROOT)/bin/gofmt"
formatting=$("$gofmt" -l ./internal ./cmd)
if [ -n "$formatting" ]; then
  echo 'Go formatting check failed' >&2
  printf '%s\n' "$formatting"
  "$gofmt" -d ./internal ./cmd
  exit 1
fi
