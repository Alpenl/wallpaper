#!/usr/bin/env bash
# 签名/公证钩子（只是钩子，无证书）。需在 macOS 上运行：
#   WPAPP_CODESIGN_ID="Developer ID Application: …"  → codesign --options runtime --timestamp
#   WPAPP_NOTARY_PROFILE=<notarytool keychain profile> → xcrun notarytool submit --wait + stapler staple
# 无证书时在 macOS 上封装 ad-hoc 签名；不代表 Developer ID 或公证。
set -euo pipefail
APP=$1
if [ -n "${WPAPP_NOTARY_PROFILE:-}" ] && [ -z "${WPAPP_CODESIGN_ID:-}" ]; then
  echo 'notarization requires WPAPP_CODESIGN_ID' >&2; exit 1
fi
if [ "$(uname)" != Darwin ]; then
  echo "sign-macos: skipped (not macOS): $APP"; exit 0
fi
if [ -z "${WPAPP_CODESIGN_ID:-}" ]; then
  codesign --force --sign - "$APP"
  codesign --verify --strict "$APP"
  echo "sign-macos: ad-hoc only; no Developer ID or notarization: $APP"
  exit 0
fi
codesign --force --options runtime --timestamp --sign "$WPAPP_CODESIGN_ID" "$APP"
codesign --verify --strict "$APP"
if [ -n "${WPAPP_NOTARY_PROFILE:-}" ]; then
  ditto -c -k --keepParent "$APP" "$APP.notary.zip"
  if [ -n "${WPAPP_NOTARY_KEYCHAIN:-}" ]; then
    xcrun notarytool submit "$APP.notary.zip" --keychain-profile "$WPAPP_NOTARY_PROFILE" --keychain "$WPAPP_NOTARY_KEYCHAIN" --wait
  else
    xcrun notarytool submit "$APP.notary.zip" --keychain-profile "$WPAPP_NOTARY_PROFILE" --wait
  fi
  xcrun stapler staple "$APP"; rm "$APP.notary.zip"
fi
