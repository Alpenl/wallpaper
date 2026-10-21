#!/usr/bin/env bash
# 交叉编译 5 个目标（CGO_ENABLED=0），产物在 dist/<os>-<arch>/，并生成 dist/SHA256SUMS。
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.1.0-mvp}
TARGETS=${TARGETS:-"windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64"}
mkdir -p dist
for t in $TARGETS; do
  os=${t%/*}; arch=${t#*/}; out=dist/$os-$arch; mkdir -p "$out"
  bin=wallpaperd; ld="-s -w -X main.version=$VERSION"
  if [ "$os" = windows ]; then bin=wallpaperd.exe; ld="$ld -H windowsgui"; fi
  echo "build $os/$arch"
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "$ld" -o "$out/$bin" ./cmd/wallpaperd
done
( cd dist && find . -type f \( -name wallpaperd -o -name wallpaperd.exe -o -name '*-setup.exe' -o -name '*.app.zip' \) -not -path './*/mpv/*' -not -path './macos-*' | sort | xargs sha256sum > SHA256SUMS )
cat dist/SHA256SUMS
