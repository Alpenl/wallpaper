#!/usr/bin/env bash
# 下载 Windows 版 mpv（shinchiro/mpv-winbuild-cmake 的 GitHub Release）到 third_party/mpv/<arch>/。
# 只下载，不提交：third_party/ 在 .gitignore 里。本脚本不会被发布流程自动调用。
# 用法：scripts/fetch-mpv.sh <amd64|arm64> <pinned-release-tag> <expected-archive-sha256>
# 也可通过环境变量 EXPECTED_SHA256 传入预期 SHA-256。
# 许可提示（非法律意见）：该构建的许可证与所含组件需在发布前核对，见 docs/design/DECISIONS_PENDING.md 的 L-1..L-4。
set -euo pipefail
ARCH=${1:-}
TAG=${2:-}
EXPECTED_SHA256=${3:-${EXPECTED_SHA256:-}}
REPO=shinchiro/mpv-winbuild-cmake

case "$ARCH" in
  amd64) UPSTREAM_ARCH=x86_64 ;;
  arm64) UPSTREAM_ARCH=aarch64 ;;
  *)
    echo "usage: $0 <amd64|arm64> <pinned-release-tag> <expected-archive-sha256>" >&2
    echo "unsupported ARCH: ${ARCH:-<empty>} (allowed: amd64, arm64)" >&2
    exit 1
    ;;
esac

if [ -z "$TAG" ] || [ "$TAG" = latest ]; then
  echo "refusing unpinned release tag '${TAG:-<empty>}'; pass an explicit release tag" >&2
  exit 1
fi
if [[ ! "$TAG" =~ ^[0-9A-Za-z._-]+$ ]]; then
  echo "invalid release tag: $TAG" >&2
  exit 1
fi
if [[ ! "$EXPECTED_SHA256" =~ ^[0-9a-fA-F]{64}$ ]]; then
  echo "missing or invalid expected SHA-256 checksum (64 hex chars required)" >&2
  exit 1
fi
EXPECTED_SHA256=$(printf '%s' "$EXPECTED_SHA256" | tr 'A-F' 'a-f')

cd "$(dirname "$0")/.."
API="https://api.github.com/repos/$REPO/releases/tags/$TAG"
JSON=$(curl -fsSL "$API")
URL=$(printf '%s' "$JSON" | python3 - "$UPSTREAM_ARCH" <<'PY'
import json, sys
arch = sys.argv[1]
prefix = f'mpv-{arch}-'
data = json.load(sys.stdin)
matches = [
    a['browser_download_url']
    for a in data.get('assets', [])
    if a.get('name', '').startswith(prefix)
    and a.get('name', '').endswith('.7z')
    and '-v3-' not in a.get('name', '')
    and not a.get('name', '').startswith('mpv-dev-')
]
if len(matches) != 1:
    raise SystemExit(f'expected exactly 1 baseline {arch} mpv .7z asset, found {len(matches)}: {matches}')
print(matches[0])
PY
)
DEST="third_party/mpv/$ARCH"
rm -rf "$DEST"; mkdir -p "$DEST"
echo "download $URL"
curl -fL --retry 3 -o "$DEST/mpv.7z" "$URL"
SUM=$(sha256sum "$DEST/mpv.7z" | awk '{print tolower($1)}')
if [ "$SUM" != "$EXPECTED_SHA256" ]; then
  rm -f "$DEST/mpv.7z"
  echo "SHA-256 mismatch for $URL: got $SUM, expected $EXPECTED_SHA256" >&2
  exit 1
fi
7z x -y -o"$DEST" "$DEST/mpv.7z" mpv.exe >/dev/null
rm "$DEST/mpv.7z"
printf 'source=%s\ntag=%s\narch=%s\narchive_sha256=%s\nmpv.exe_sha256=%s\nfetched=%s\n' \
  "$URL" "$TAG" "$ARCH" "$SUM" "$(sha256sum "$DEST/mpv.exe" | awk '{print $1}')" "$(date -Iseconds)" > "$DEST/SOURCE.txt"
cat "$DEST/SOURCE.txt"
