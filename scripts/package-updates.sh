#!/usr/bin/env bash
# Optional signed updater manifests. This does not deploy/configure an update feed.
set -euo pipefail
[ -n "${WPAPP_UPDATE_KEY:-}" ] && [ -n "${WPAPP_UPDATE_PUB:-}" ] || exit 0
cd "$(dirname "$0")/.."
source scripts/package-common.sh
package_validate
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
chmod 700 "$scratch"
printf '%s' "$WPAPP_UPDATE_KEY" > "$scratch/update.key"
printf '%s' "$WPAPP_UPDATE_PUB" > "$scratch/update.pub"
go build -o "$scratch/wpupdate" ./cmd/wpupdate
for target in windows-amd64 windows-arm64 darwin-amd64 darwin-arm64; do
  mkdir -p "$scratch/payload/$target"
  case "$target" in
    windows-*)
      arch=${target#windows-}
      staged="dist/windows-packaged-$arch"
      [ -s "$staged/wallpaperd.exe" ] || {
        echo "missing $staged/wallpaperd.exe; run scripts/package-windows.sh first" >&2
        exit 1
      }
      if [ -n "${WPAPP_SIGN_CMD:-}" ]; then
        package_check_pe_signed "$staged/wallpaperd.exe"
      fi
      cp -R "$staged/." "$scratch/payload/$target/"
      ;;
    darwin-*)
      arch=${target#darwin-}
      contents="dist/macos-$arch/Wallpaper.app/Contents"
      [ -s "$contents/MacOS/wallpaperd" ] || {
        echo "missing $contents/MacOS/wallpaperd; run scripts/package-macos.sh first" >&2
        exit 1
      }
      # Mirror Wallpaper.app/Contents layout (MacOS/wallpaperd, Info.plist,
      # Resources/*, and _CodeSignature/* when signed) to match installDir(exe).
      cp -R "$contents/." "$scratch/payload/$target/"
      ;;
  esac
  "$scratch/wpupdate" manifest -dir "$scratch/payload/$target" -version "$VERSION" -platform "$target" -key "$scratch/update.key" -out "$scratch/feed"
  "$scratch/wpupdate" verify -feed "$scratch/feed" -platform "$target" -pub "$scratch/update.pub"
done
tar -czf "$RELEASE_DIR/Wallpaper-$VERSION-updates.tar.gz" -C "$scratch/feed" .
