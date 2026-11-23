#!/usr/bin/env bash
# 签名钩子（只是钩子，仓库里没有证书）。设置 WPAPP_SIGN_CMD 时对每个参数文件执行它，例如：
#   WPAPP_SIGN_CMD='osslsigncode sign -h sha256 -ts http://timestamp.digicert.com -pkcs12 cert.pfx -readpass "$PFX_PASS_FILE" -in "$1" -out "$1.signed" && mv "$1.signed" "$1"'
# 也兼容 {} 占位符（会安全替换为 "$1" 并通过位置参数传入，避免路径字符注入）；同时导出 WPAPP_SIGN_FILE。
# 未设置时只打印提示并返回 0（产物未签名，SmartScreen 会警告）。
set -euo pipefail
if [ -z "${WPAPP_SIGN_CMD:-}" ]; then echo "sign-windows: WPAPP_SIGN_CMD not set, leaving unsigned: $*"; exit 0; fi
placeholder='"$1"'
cmd=${WPAPP_SIGN_CMD//\{\}/$placeholder}
for f in "$@"; do
  WPAPP_SIGN_FILE="$f" sh -c "$cmd" sign-windows "$f"
done
