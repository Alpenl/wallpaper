#!/usr/bin/env bash
# 一键发布流水线：测试 → 交叉编译 → 下载 mpv → Windows NSIS 安装器 → macOS .app.zip → 签名钩子 → SHA256SUMS → （可选）增量更新源。
#
# 环境变量（都可选；没有证书时产物未签名，流程照常完成）：
#   VERSION                 版本号（默认 0.2.1-mvp）
#   WPAPP_SIGN_CMD          Windows 签名命令模板，{} 为文件（见 scripts/sign-windows.sh）
#   WPAPP_CODESIGN_ID       macOS Developer ID（只在 macOS 上生效，见 scripts/sign-macos.sh）
#   WPAPP_NOTARY_PROFILE    macOS notarytool 钥匙串配置
#   WPAPP_UPDATE_PUB        更新公钥（编进程序）；WPAPP_UPDATE_KEY 私钥文件路径（生成 dist/feed/）
#   SKIP_TESTS=1            跳过测试
set -euo pipefail
cd "$(dirname "$0")/.."
export VERSION=${VERSION:-0.2.1-mvp}
if [ "${SKIP_TESTS:-}" != 1 ]; then CGO_ENABLED=1 go test -race ./...; fi
scripts/build.sh
[ -f third_party/mpv/x86_64/mpv.exe ] || scripts/fetch-mpv.sh x86_64
scripts/package-windows.sh
scripts/package-macos.sh
if [ -n "${WPAPP_UPDATE_KEY:-}" ]; then
  go build -o dist/wpupdate ./cmd/wpupdate
  for t in windows-amd64 windows-arm64 linux-amd64; do
    dist/wpupdate manifest -dir "dist/$t" -version "$VERSION" -platform "$t" -key "$WPAPP_UPDATE_KEY" -out dist/feed
  done
  for a in arm64 amd64; do
    dist/wpupdate manifest -dir "dist/macos-$a/Wallpaper.app/Contents" -version "$VERSION" -platform "darwin-$a" -key "$WPAPP_UPDATE_KEY" -out dist/feed
  done
fi
( cd dist && sha256sum Wallpaper-"$VERSION"-* linux-amd64/wallpaperd > SHA256SUMS && cat SHA256SUMS )
