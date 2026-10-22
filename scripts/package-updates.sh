#!/usr/bin/env bash
# Optional signed updater manifests. This does not deploy/configure an update feed.
set -euo pipefail
[ -n "${WPAPP_UPDATE_KEY:-}" ] && [ -n "${WPAPP_UPDATE_PUB:-}" ] || exit 0
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
chmod 700 "$scratch"
printf '%s' "$WPAPP_UPDATE_KEY" > "$scratch/update.key"
printf '%s' "$WPAPP_UPDATE_PUB" > "$scratch/update.pub"
go build -o "$scratch/wpupdate" ./cmd/wpupdate
for target in windows-amd64 windows-arm64 darwin-amd64 darwin-arm64 linux-amd64; do
  mkdir -p "$scratch/payload/$target"
  binary=wallpaperd
  case "$target" in
    windows-*) binary=wallpaperd.exe ;;
  esac
  cp "dist/$target/$binary" "$scratch/payload/$target/$binary"
  "$scratch/wpupdate" manifest -dir "$scratch/payload/$target" -version "$VERSION" -platform "$target" -key "$scratch/update.key" -out "$scratch/feed"
  "$scratch/wpupdate" verify -feed "$scratch/feed" -platform "$target" -pub "$scratch/update.pub"
done
tar -czf "$RELEASE_DIR/Wallpaper-$VERSION-updates.tar.gz" -C "$scratch/feed" .
