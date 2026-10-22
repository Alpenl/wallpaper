#!/usr/bin/env bash
# Reproducible production checks; historical prototype results are separate.
set -euo pipefail
cd "$(dirname "$0")/.."
formatting=$(find cmd internal -name '*.go' -type f -exec gofmt -l {} +)
if [ -n "$formatting" ]; then
  printf '%s\n' "$formatting"
  echo 'Go formatting check failed' >&2
  exit 1
fi
go test -race ./...
go vet -unsafeptr=false ./...
CGO_ENABLED=0 go build ./cmd/...
python3 scripts/check-docs.py
# The separate ui branch is design-only. This master-based application and its
# behavior checks are exclusively native Go/MyGo; no React runtime is included.
if [ "${CROSS_BUILD:-0}" = 1 ]; then
  bash scripts/build.sh
fi
