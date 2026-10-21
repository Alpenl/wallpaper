#!/usr/bin/env bash
# 下载 Windows 版 mpv（shinchiro/mpv-winbuild-cmake 的 GitHub Release）到 third_party/mpv/<arch>/。
# 只下载，不提交：third_party/ 在 .gitignore 里。打包脚本从这里取 mpv.exe。
# 用法：scripts/fetch-mpv.sh [x86_64|aarch64] [release-tag，默认 latest]
# 许可提示（非法律意见）：该构建的许可证与所含组件需在发布前核对，见 wallpaper_docs/DECISIONS_PENDING.md 的 L-1..L-4。
set -euo pipefail
ARCH=${1:-x86_64}
TAG=${2:-latest}
REPO=shinchiro/mpv-winbuild-cmake
cd "$(dirname "$0")/.."
API="https://api.github.com/repos/$REPO/releases/$TAG"
[ "$TAG" != latest ] && API="https://api.github.com/repos/$REPO/releases/tags/$TAG"
JSON=$(curl -fsSL "$API")
URL=$(printf '%s' "$JSON" | python3 -c "import json,sys;d=json.load(sys.stdin);print(next(a['browser_download_url'] for a in d['assets'] if a['name'].startswith('mpv-$ARCH-') and a['name'].endswith('.7z')))")
DEST=third_party/mpv/$ARCH
rm -rf "$DEST"; mkdir -p "$DEST"
echo "download $URL"
curl -fL --retry 3 -o "$DEST/mpv.7z" "$URL"
SUM=$(sha256sum "$DEST/mpv.7z" | cut -d' ' -f1)
7z x -y -o"$DEST" "$DEST/mpv.7z" mpv.exe >/dev/null
rm "$DEST/mpv.7z"
printf 'source=%s\narchive_sha256=%s\nmpv.exe_sha256=%s\nfetched=%s\n' "$URL" "$SUM" "$(sha256sum "$DEST/mpv.exe" | cut -d' ' -f1)" "$(date -Iseconds)" > "$DEST/SOURCE.txt"
cat "$DEST/SOURCE.txt"
