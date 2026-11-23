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
  cp assets/icon/AppIcon.icns "$app/Contents/Resources/AppIcon.icns"
  short_version=${VERSION%%-*}
  sed -e "s/@SHORT_VERSION@/${short_version}/g" -e "s/@BUNDLE_VERSION@/${VERSION}/g" \
    packaging/macos/Info.plist.tmpl > "$app/Contents/Info.plist"
  printf 'APPL????' > "$app/Contents/PkgInfo"
  if [ "$(uname)" = Darwin ]; then plutil -lint "$app/Contents/Info.plist"; fi
  scripts/sign-macos.sh "$app"
  out="$RELEASE_DIR/Wallpaper-$VERSION-macos-$arch.app.zip"
  rm -f "$out"
  if [ "$(uname)" = Darwin ]; then
    ditto -c -k --sequesterRsrc --keepParent "$app" "$out"
  else
    # Cross-packaging fallback: preserve Unix modes and both file/directory symlinks
    # without following symlinks (ZipInfo.from_file follows symlinks and corrupts bundles).
    python3 - "dist/macos-$arch" "$out" <<'PY'
from pathlib import Path
import os, sys, time, zipfile
root, out = Path(sys.argv[1]), Path(sys.argv[2])
app = root / 'Wallpaper.app'
def zip_info(name, st):
    tm = time.localtime(st.st_mtime)
    dt = (max(tm.tm_year, 1980), tm.tm_mon, tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec)
    info = zipfile.ZipInfo(name, date_time=dt)
    info.create_system = 3
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = (st.st_mode & 0xFFFF) << 16
    return info
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as archive:
    for dirpath, dirnames, filenames in os.walk(app, followlinks=False):
        dirnames.sort()
        filenames.sort()
        current = Path(dirpath)
        real_dirs = []
        for d in dirnames:
            p = current / d
            if p.is_symlink():
                archive.writestr(zip_info(p.relative_to(root).as_posix(), p.lstat()), os.readlink(p).encode())
            else:
                real_dirs.append(d)
        dirnames[:] = real_dirs
        for f in filenames:
            p = current / f
            st = p.lstat()
            name = p.relative_to(root).as_posix()
            data = os.readlink(p).encode() if p.is_symlink() else p.read_bytes()
            archive.writestr(zip_info(name, st), data)
PY
  fi
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
    if [ -n "${WPAPP_CODESIGN_ID:-}" ]; then
      codesign --force --timestamp --sign "$WPAPP_CODESIGN_ID" "$dmg"
      codesign --verify --strict "$dmg"
      if [ -n "${WPAPP_NOTARY_PROFILE:-}" ]; then
        if [ -n "${WPAPP_NOTARY_KEYCHAIN:-}" ]; then
          xcrun notarytool submit "$dmg" --keychain-profile "$WPAPP_NOTARY_PROFILE" --keychain "$WPAPP_NOTARY_KEYCHAIN" --wait
        else
          xcrun notarytool submit "$dmg" --keychain-profile "$WPAPP_NOTARY_PROFILE" --wait
        fi
        xcrun stapler staple "$dmg"
        xcrun stapler validate "$dmg"
      fi
    fi
    hdiutil verify "$dmg"
    rm -rf "$stage"
    trap - EXIT
  fi
done
