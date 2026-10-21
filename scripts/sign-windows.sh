#!/usr/bin/env bash
# 签名钩子（只是钩子，仓库里没有证书）。设置 WPAPP_SIGN_CMD 时对每个参数文件执行它，例如：
#   WPAPP_SIGN_CMD='osslsigncode sign -pkcs12 cert.pfx -pass "$PFX_PASS" -t http://timestamp.digicert.com -in {} -out {}.signed && mv {}.signed {}'
# 未设置时只打印提示并返回 0（产物未签名，SmartScreen 会警告）。
set -euo pipefail
if [ -z "${WPAPP_SIGN_CMD:-}" ]; then echo "sign-windows: WPAPP_SIGN_CMD not set, leaving unsigned: $*"; exit 0; fi
for f in "$@"; do sh -c "${WPAPP_SIGN_CMD//\{\}/$f}"; done
