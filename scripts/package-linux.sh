#!/usr/bin/env bash
# Linux amd64 portable native app; mpv/ffmpeg remain system dependencies.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/package-common.sh
package_validate
package_require_metadata
for arch in ${ARCHES:-amd64}; do
  [ "$arch" = amd64 ] || { echo "unsupported Linux arch: $arch" >&2; exit 1; }
  bin=dist/linux-$arch/wallpaperd
  package_check_binary "$bin" "linux/$arch"
  python3 - "$bin" "$RELEASE_DIR/Wallpaper-$VERSION-linux-$arch.tar.gz" "$VERSION" "$arch" <<'PY'
from pathlib import Path
import tarfile, sys
binary, output, version, arch = sys.argv[1:]
root = f'Wallpaper-{version}-linux-{arch}'
with tarfile.open(output, 'w:gz') as archive:
    def add(path, name, executable=False):
        info = archive.gettarinfo(str(path), root + '/' + name)
        info.uid = info.gid = 0
        info.uname = info.gname = ''
        info.mode = 0o755 if executable else 0o644
        with open(path, 'rb') as source:
            archive.addfile(info, source)
    add(binary, 'wallpaperd', True)
    for name in ['RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt']:
        add(Path('dist/package-metadata') / name, name)
PY
  ls -la "$RELEASE_DIR/Wallpaper-$VERSION-linux-$arch.tar.gz"
done
