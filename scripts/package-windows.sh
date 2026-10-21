#!/usr/bin/env bash
# 用 NSIS 生成安装器：dist/Wallpaper-<ver>-windows-<arch>-setup.exe。需要 makensis（apt install nsis）。
# arm64 安装器默认不带 mpv（shinchiro 有 aarch64 构建：scripts/fetch-mpv.sh aarch64 后会自动带上）。
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.2.0-mvp}
for arch in ${ARCHES:-amd64 arm64}; do
  src=dist/windows-$arch
  [ -f "$src/wallpaperd.exe" ] || { echo "missing $src/wallpaperd.exe (run scripts/build.sh)"; exit 1; }
  m=x86_64; [ "$arch" = arm64 ] && m=aarch64
  if [ -f third_party/mpv/$m/mpv.exe ]; then mkdir -p "$src/mpv"; cp third_party/mpv/$m/mpv.exe third_party/mpv/$m/SOURCE.txt "$src/mpv/"; fi
  scripts/sign-windows.sh "$src/wallpaperd.exe"
  out=dist/Wallpaper-$VERSION-windows-$arch-setup.exe
  makensis -V2 -DVERSION="$VERSION" -DARCH="$arch" -DSRC="$PWD/$src" -DOUT="$PWD/$out" packaging/windows/installer.nsi
  scripts/sign-windows.sh "$out"
  ls -la "$out"
done
