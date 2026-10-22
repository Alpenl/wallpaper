#!/usr/bin/env bash
# Shared packaging validation. Source from the repository root.
package_validate() {
  VERSION=${VERSION:?Set VERSION to the release semver}
  RELEASE_DIR=${RELEASE_DIR:-dist/release}
  export VERSION RELEASE_DIR
  [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]] || {
    echo "invalid release VERSION: $VERSION" >&2; return 1;
  }
  # Keep generated output inside dist and refuse symlink traversal outside it.
  RELEASE_DIR=$(python3 - "$RELEASE_DIR" <<'PY'
from pathlib import Path
import sys
if Path('dist').is_symlink():
    raise SystemExit('dist/ must not be a symlink')
base, out = Path('dist').resolve(), Path(sys.argv[1]).resolve()
if base not in out.parents or out == base:
    raise SystemExit('RELEASE_DIR must be a dedicated directory below dist/')
out.mkdir(parents=True, exist_ok=True)
print(out)
PY
  ) || return 1
  export RELEASE_DIR
  if [ "${BUNDLE_MPV:-0}" != 0 ]; then
    echo 'mpv bundling is disabled until its exact source/license distribution is reviewed. Install mpv separately: https://mpv.io/installation/' >&2
    return 1
  fi
}

package_require_metadata() {
  for name in LICENSE RELEASE-NOTES.txt BUILD-INFO.txt THIRD-PARTY-NOTICES.txt; do
    [ -s "dist/package-metadata/$name" ] || {
      echo 'missing package metadata; run scripts/package-metadata.sh first (or scripts/release.sh)' >&2
      return 1
    }
  done
  grep -Fqx "version=$VERSION" dist/package-metadata/BUILD-INFO.txt || {
    echo 'package metadata version mismatch; run scripts/package-metadata.sh' >&2; return 1;
  }
}

package_check_binary() {
  local binary=$1 target=$2
  [ -s "$binary" ] || { echo "missing $binary; run scripts/build.sh" >&2; return 1; }
  # Inspect Go build info without executing the foreign binary.
  python3 - "$binary" "$VERSION" "$target" <<'PY'
from pathlib import Path
import subprocess, sys
binary, version, target = sys.argv[1:]
info = subprocess.check_output(['go', 'version', '-m', binary], text=True)
os, arch = target.split('/')
for marker in ['CGO_ENABLED=0', 'GOOS=' + os, 'GOARCH=' + arch]:
    if not any(line.strip() == 'build\t' + marker for line in info.splitlines()):
        raise SystemExit(f'{binary}: missing/mismatched build metadata {marker}')
# Go -trimpath deliberately omits linker flags from build info. Inspect the
# embedded version string; release-verify also executes Linux -version.
if version.encode() not in Path(binary).read_bytes():
    raise SystemExit(f'{binary}: requested version string not found')
PY
}
