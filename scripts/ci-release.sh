#!/usr/bin/env bash
# Hosted macOS credential provisioning; every secret file lives outside checkout.
set -euo pipefail
scratch=$(mktemp -d)
keychain="$scratch/release.keychain-db"
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  rm -rf "$scratch"
}
trap cleanup EXIT
export PATH="$(brew --prefix coreutils)/libexec/gnubin:$PATH"
if [ -n "${WPAPP_WINDOWS_PFX_BASE64:-}" ]; then
  printf '%s' "$WPAPP_WINDOWS_PFX_BASE64" | base64 --decode > "$scratch/windows.pfx"
  export WPAPP_WINDOWS_PFX="$scratch/windows.pfx"
  export WPAPP_WINDOWS_PASSWORD_FILE="$scratch/windows-password"
  printf '%s' "${WPAPP_WINDOWS_PFX_PASSWORD:-}" > "$WPAPP_WINDOWS_PASSWORD_FILE"
  if [ -z "${WPAPP_SIGN_CMD:-}" ]; then
    export WPAPP_SIGN_CMD='osslsigncode sign -pkcs12 "$WPAPP_WINDOWS_PFX" -readpass "$WPAPP_WINDOWS_PASSWORD_FILE" -in {} -out {}.signed && mv {}.signed {}'
  fi
fi
if [ -n "${WPAPP_MACOS_P12_BASE64:-}" ] && [ -n "${WPAPP_CODESIGN_ID:-}" ]; then
  printf '%s' "$WPAPP_MACOS_P12_BASE64" | base64 --decode > "$scratch/macos.p12"
  password=$(openssl rand -hex 32)
  security create-keychain -p "$password" "$keychain"
  security set-keychain-settings -lut 21600 "$keychain"
  security unlock-keychain -p "$password" "$keychain"
  security import "$scratch/macos.p12" -P "${WPAPP_MACOS_P12_PASSWORD:-}" -A -t cert -f pkcs12 -k "$keychain"
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$password" "$keychain" >/dev/null
  security list-keychains -d user -s "$keychain" login.keychain-db
  if [ -n "${WPAPP_APPLE_ID:-}" ] && [ -n "${WPAPP_APPLE_PASSWORD:-}" ] && [ -n "${WPAPP_APPLE_TEAM_ID:-}" ]; then
    export WPAPP_NOTARY_PROFILE=${WPAPP_NOTARY_PROFILE:-wallpaper-release}
    xcrun notarytool store-credentials "$WPAPP_NOTARY_PROFILE" --apple-id "$WPAPP_APPLE_ID" --password "$WPAPP_APPLE_PASSWORD" --team-id "$WPAPP_APPLE_TEAM_ID" --keychain "$keychain"
    export WPAPP_NOTARY_KEYCHAIN="$keychain"
  else
    unset WPAPP_NOTARY_PROFILE
  fi
else
  unset WPAPP_CODESIGN_ID WPAPP_NOTARY_PROFILE
fi
# An incomplete updater key pair must not turn a no-secrets build into a failure.
if [ -z "${WPAPP_UPDATE_KEY:-}" ] || [ -z "${WPAPP_UPDATE_PUB:-}" ]; then
  unset WPAPP_UPDATE_KEY WPAPP_UPDATE_PUB
fi
bash scripts/release.sh
