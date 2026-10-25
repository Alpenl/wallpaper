#!/usr/bin/env bash
# Native macOS .app archives; no Apple Developer ID/notarization by default.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/package-common.sh
package_validate
package_require_metadata
if [ -n "${WPAPP_CODESIGN_ID:-}${WPAPP_NOTARY_PROFILE:-}" ] && [ "$(uname)" != Darwin ]; then
  echo 'macOS signing/notarization requires a macOS host' >&2; exit 1
fi
for arch in ${ARCHES:-arm64 amd64}; do
  case "$arch" in amd64|arm64) ;; *) echo "unsupported macOS arch: $arch" >&2; exit 1;; esac
  bin=dist/darwin-$arch/wallpaperd
  package_check_binary "$bin" "darwin/$arch"
  app=dist/macos-$arch/Wallpaper.app
  rm -rf "$app"; mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  cp "$bin" "$app/Contents/MacOS/wallpaperd"; chmod 755 "$app/Contents/MacOS/wallpaperd"
  cp dist/package-metadata/LICENSE dist/package-metadata/{RELEASE-NOTES,BUILD-INFO,THIRD-PARTY-NOTICES}.txt "$app/Contents/Resources/"
  sed "s/@VERSION@/${VERSION%%-*}/g" packaging/macos/Info.plist.tmpl > "$app/Contents/Info.plist"
  printf 'APPL????' > "$app/Contents/PkgInfo"
  if [ "$(uname)" = Darwin ]; then plutil -lint "$app/Contents/Info.plist"; fi
  scripts/sign-macos.sh "$app"
  out="$RELEASE_DIR/Wallpaper-$VERSION-macos-$arch.app.zip"
  # Explicit Unix metadata preserves executable permissions and symbolic links.
  python3 - "dist/macos-$arch" "$out" <<'PY'
from pathlib import Path
import os, sys, zipfile
root, out = Path(sys.argv[1]), Path(sys.argv[2])
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as archive:
    for path in sorted((root / 'Wallpaper.app').rglob('*')):
        if path.is_dir() and not path.is_symlink():
            continue
        name = path.relative_to(root).as_posix()
        info = zipfile.ZipInfo.from_file(path, name)
        info.create_system = 3
        info.compress_type = zipfile.ZIP_DEFLATED
        info.external_attr = (path.lstat().st_mode & 0xFFFF) << 16
        data = os.readlink(path).encode() if path.is_symlink() else path.read_bytes()
        archive.writestr(info, data)
PY
  ls -la "$out"
  # Optional local drag-to-Applications disk image; ZIP remains the CI asset.
  if [ "${MACOS_DMG:-0}" = 1 ]; then
    [ "$(uname)" = Darwin ] || { echo 'DMG creation requires macOS' >&2; exit 1; }
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' EXIT
    ditto "$app" "$stage/Wallpaper.app"
    ln -s /Applications "$stage/Applications"
    dmg="$RELEASE_DIR/Wallpaper-$VERSION-macos-$arch.dmg"
    hdiutil create -ov -format UDZO -volname "Wallpaper $VERSION" -srcfolder "$stage" "$dmg"
    hdiutil verify "$dmg"
    rm -rf "$stage"
    trap - EXIT
  fi
done
