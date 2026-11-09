#!/usr/bin/env bash
# M1: four native targets, no packaging or release metadata changes.
set -euo pipefail
cd "$(dirname "$0")/.."
for target in darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}
  arch=${target#*/}
  out="dist/$os-$arch/wallpaperd"
  flags='-s -w'
  if [ "$os" = windows ]; then out+=.exe; flags+=' -H windowsgui'; fi
  mkdir -p "$(dirname "$out")"
  echo "build $target"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$flags" -o "$out" ./cmd/wallpaperd
done
