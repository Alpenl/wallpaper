#!/usr/bin/env bash
# Native release: checks -> five cross-builds -> installers/archives -> verification.
# VERSION=1.2.3 SKIP_TESTS=1 scripts/release.sh
# Final, flat upload directory: dist/release/ (not the entire dist/ directory).
# No mpv/ffmpeg download, signing identity, or production update key is needed.
# Signed builds require explicit ALLOW_SIGNING=1 plus the existing signing hooks.
set -euo pipefail
cd "$(dirname "$0")/.."
export VERSION=${VERSION:?Set VERSION to the release semver}
export RELEASE_DIR=${RELEASE_DIR:-dist/release}
source scripts/package-common.sh
package_validate
for tool in go python3 makensis; do
  command -v "$tool" >/dev/null || { echo "missing release tool: $tool" >&2; exit 1; }
done
if [ "${ALLOW_SIGNING:-0}" != 1 ]; then
  for key in WPAPP_SIGN_CMD WPAPP_CODESIGN_ID WPAPP_NOTARY_PROFILE; do
    [ -z "${!key:-}" ] || { echo "$key requires explicit ALLOW_SIGNING=1" >&2; exit 1; }
  done
fi
if [ -n "${WPAPP_CODESIGN_ID:-}${WPAPP_NOTARY_PROFILE:-}" ] && [ "$(uname)" != Darwin ]; then
  echo 'macOS signing/notarization requires a macOS host; refusing a misleading signed label' >&2
  exit 1
fi
if [ -n "${WPAPP_NOTARY_PROFILE:-}" ] && [ -z "${WPAPP_CODESIGN_ID:-}" ]; then
  echo 'notarization requires WPAPP_CODESIGN_ID' >&2; exit 1
fi
bash scripts/release-self-test.sh
if [ "${SKIP_TESTS:-0}" != 1 ]; then bash scripts/check.sh; fi
# Never inherit a partial TARGETS/ARCHES value and label it a complete release.
TARGETS='windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64' bash scripts/build.sh
bash scripts/package-metadata.sh
# Remove only generated files in this dedicated directory; never arbitrary paths.
python3 - "$RELEASE_DIR" <<'PY'
from pathlib import Path
import sys
root = Path(sys.argv[1])
for p in root.iterdir():
    if p.is_file() and (p.name.startswith('Wallpaper-') or p.name in {
        'SHA256SUMS', 'RELEASE-NOTES.txt', 'BUILD-INFO.txt', 'THIRD-PARTY-NOTICES.txt'}):
        p.unlink()
PY
ARCHES='amd64 arm64' bash scripts/package-windows.sh
ARCHES='amd64 arm64' bash scripts/package-macos.sh
ARCHES='amd64' bash scripts/package-linux.sh
bash scripts/package-updates.sh
cp dist/package-metadata/{RELEASE-NOTES,BUILD-INFO}.txt "$RELEASE_DIR/"
python3 - "$RELEASE_DIR" <<'PY'
from pathlib import Path
import hashlib, sys
root = Path(sys.argv[1])
files = sorted(p for p in root.iterdir() if p.is_file() and p.name != 'SHA256SUMS')
with (root / 'SHA256SUMS').open('w') as out:
    for path in files:
        out.write(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n')
PY
bash scripts/release-verify.sh
cat "$RELEASE_DIR/SHA256SUMS"
