#!/usr/bin/env bash
# Per-user NSIS installers. mpv is deliberately not bundled by this release.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/package-common.sh
package_validate
package_require_metadata
command -v makensis >/dev/null || { echo 'missing makensis (Debian/Ubuntu: apt-get install nsis)' >&2; exit 1; }
mkdir -p dist/package-staging
stage=$(mktemp -d "$PWD/dist/package-staging/windows.XXXXXX")
trap 'rm -rf "$stage"' EXIT
for arch in ${ARCHES:-amd64 arm64}; do
  case "$arch" in amd64|arm64) ;; *) echo "unsupported Windows arch: $arch" >&2; exit 1;; esac
  bin=dist/windows-$arch/wallpaperd.exe
  package_check_binary "$bin" "windows/$arch"
  src="$stage/$arch"
  mkdir -p "$src"
  cp "$bin" "$src/wallpaperd.exe"
  cp dist/package-metadata/LICENSE dist/package-metadata/{RELEASE-NOTES,BUILD-INFO,THIRD-PARTY-NOTICES}.txt "$src/"
  # Fresh staging never inherits third_party/mpv or a previous dist/*/mpv.
  scripts/sign-windows.sh "$src/wallpaperd.exe"
  out="$RELEASE_DIR/Wallpaper-$VERSION-windows-$arch-setup.exe"
  # Git Bash's $PWD and staging paths are /c/...; native makensis.exe needs
  # Windows paths for every -D value that names a file.
  icon="$PWD/assets/icon/wallpaper.ico"
  # SRC must carry a platform separator because installer.nsi appends the
  # filename directly (native makensis rejects mixed separators inside File).
  src_arg="$src/"
  out_arg="$out"
  if command -v cygpath >/dev/null 2>&1; then
    icon=$(cygpath -w "$icon")
    src_arg="$(cygpath -w "$src")\\"
    out_arg=$(cygpath -w "$out")
  fi
  makensis -V2 -DVERSION="$VERSION" -DARCH="$arch" -DSRC="$src_arg" -DOUT="$out_arg" \
    -DICON="$icon" packaging/windows/installer.nsi
  scripts/sign-windows.sh "$out"
  ls -la "$out"
done
