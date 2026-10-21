#!/usr/bin/env bash
# 交叉编译 5 个目标（CGO_ENABLED=0），产物在 dist/<os>-<arch>/，并生成 dist/SHA256SUMS。
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.2.1-mvp}
TARGETS=${TARGETS:-"windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64"}
mkdir -p dist
for t in $TARGETS; do
  os=${t%/*}; arch=${t#*/}; out=dist/$os-$arch; mkdir -p "$out"
  bin=wallpaperd; ld="-s -w -X main.version=$VERSION"
  # F-15：更新公钥（wpupdate keygen 生成的 update.pub 内容）；没有则这个构建不能在线更新
  if [ -n "${WPAPP_UPDATE_PUB:-}" ]; then ld="$ld -X main.updatePubKey=$WPAPP_UPDATE_PUB"; fi
  # 签名流水线（WPAPP_SIGN_CMD / WPAPP_CODESIGN_ID）存在时标记为签名构建，界面不再提示“未签名”
  if { [ "$os" = windows ] && [ -n "${WPAPP_SIGN_CMD:-}" ]; } || { [ "$os" = darwin ] && [ -n "${WPAPP_CODESIGN_ID:-}" ]; }; then ld="$ld -X main.signedBuild=1"; fi
  if [ "$os" = windows ]; then bin=wallpaperd.exe; ld="$ld -H windowsgui"; fi
  echo "build $os/$arch"
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "$ld" -o "$out/$bin" ./cmd/wallpaperd
done
( cd dist && find . -type f \( -name wallpaperd -o -name wallpaperd.exe -o -name '*-setup.exe' -o -name '*.app.zip' \) -not -path './*/mpv/*' -not -path './macos-*' | sort | xargs sha256sum > SHA256SUMS )
cat dist/SHA256SUMS
