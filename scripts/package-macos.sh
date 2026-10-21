#!/usr/bin/env bash
# 生成 dist/Wallpaper-<ver>-macos-<arch>.app.zip（每个架构一个；没有 lipo 时不做 universal）。
# LSUIElement=true：只有菜单栏图标，没有 Dock 图标。签名/公证走 scripts/sign-macos.sh（只在 macOS 上生效）。
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.2.0-mvp}
for arch in ${ARCHES:-arm64 amd64}; do
  bin=dist/darwin-$arch/wallpaperd
  [ -f "$bin" ] || { echo "missing $bin (run scripts/build.sh)"; exit 1; }
  app=dist/macos-$arch/Wallpaper.app
  rm -rf "$app"; mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  cp "$bin" "$app/Contents/MacOS/wallpaperd"; chmod +x "$app/Contents/MacOS/wallpaperd"
  sed "s/@VERSION@/${VERSION%%-*}/g" packaging/macos/Info.plist.tmpl > "$app/Contents/Info.plist"
  printf 'APPL????' > "$app/Contents/PkgInfo"
  scripts/sign-macos.sh "$app"
  out=$PWD/dist/Wallpaper-$VERSION-macos-$arch.app.zip
  rm -f "$out"
  # 用 Python 打 zip 以保留可执行位与符号链接（盒子上没有 zip/ditto）
  python3 - "dist/macos-$arch" "$out" <<'PY'
import os, sys, zipfile, stat
root, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for d, _, fs in os.walk(os.path.join(root, "Wallpaper.app")):
        for f in sorted(fs):
            p = os.path.join(d, f); arc = os.path.relpath(p, root)
            zi = zipfile.ZipInfo.from_file(p, arc); zi.compress_type = zipfile.ZIP_DEFLATED
            zi.external_attr = (os.stat(p).st_mode & 0xFFFF) << 16
            z.writestr(zi, open(p, "rb").read())
PY
  ls -la "$out"
done
