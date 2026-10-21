#!/usr/bin/env bash
# 签名/公证钩子（只是钩子，无证书）。需在 macOS 上运行：
#   WPAPP_CODESIGN_ID="Developer ID Application: …"  → codesign --options runtime --timestamp
#   WPAPP_NOTARY_PROFILE=<notarytool keychain profile> → xcrun notarytool submit --wait + stapler staple
# 未设置或不在 macOS 上时只打印提示并返回 0（未签名的 .app 需要用户右键「打开」）。
set -euo pipefail
APP=$1
if [ "$(uname)" != Darwin ] || [ -z "${WPAPP_CODESIGN_ID:-}" ]; then echo "sign-macos: skipped (not macOS or WPAPP_CODESIGN_ID unset): $APP"; exit 0; fi
codesign --force --options runtime --timestamp --sign "$WPAPP_CODESIGN_ID" "$APP"
if [ -n "${WPAPP_NOTARY_PROFILE:-}" ]; then
  ditto -c -k --keepParent "$APP" "$APP.notary.zip"
  xcrun notarytool submit "$APP.notary.zip" --keychain-profile "$WPAPP_NOTARY_PROFILE" --wait
  xcrun stapler staple "$APP"; rm "$APP.notary.zip"
fi
