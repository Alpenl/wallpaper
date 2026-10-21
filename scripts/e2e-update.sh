#!/usr/bin/env bash
# F-15 端到端：v1 安装目录 → 本地更新源（v2，只改了 wallpaperd）→ -update-apply 增量更新 → -update-rollback 回滚。
# 只用 127.0.0.1 的 http（客户端只允许 https 或本机回环）。需要 python3。
set -euo pipefail
cd "$(dirname "$0")/.."
W=$(mktemp -d); trap 'kill $SRV 2>/dev/null || true; rm -rf "$W"' EXIT
go build -o "$W/wpupdate" ./cmd/wpupdate
"$W/wpupdate" keygen -o "$W/keys" >/dev/null
PUB=$(cat "$W/keys/update.pub")
build() { CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$1 -X main.updatePubKey=$PUB" -o "$2/wallpaperd" ./cmd/wallpaperd; }
mkdir -p "$W/inst/mpv" "$W/v2/mpv"
build 0.1.0 "$W/inst"; build 0.2.0 "$W/v2"
head -c 2000000 /dev/urandom > "$W/inst/mpv/mpv"; cp "$W/inst/mpv/mpv" "$W/v2/mpv/mpv"   # 不变的大文件，不应被下载
"$W/wpupdate" manifest -dir "$W/v2" -version 0.2.0 -platform linux-amd64 -key "$W/keys/update.key" -out "$W/feed" >/dev/null
PORT=$((20000 + RANDOM % 20000))
( cd "$W/feed" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >/dev/null 2>&1 ) & SRV=$!
sleep 1
export WPAPP_HOME="$W/home"
pass=0; fail=0
check() { if eval "$2"; then echo "PASS $1"; pass=$((pass+1)); else echo "FAIL $1"; fail=$((fail+1)); fi; }
check "v1 版本" '[[ "$("$W/inst/wallpaperd" -version)" == *"0.1.0"* ]]'
out=$("$W/inst/wallpaperd" -update-apply -feed "http://127.0.0.1:$PORT")
echo "  $out"
check "更新到 0.2.0" '[[ "$("$W/inst/wallpaperd" -version)" == *"0.2.0"* ]]'
check "增量：只下载变化的 wallpaperd（不含 2 MB 的 mpv）" '[[ "$out" == *"1 files changed"* ]]'
check "再次检查已是最新" '[[ "$("$W/inst/wallpaperd" -update-apply -feed "http://127.0.0.1:$PORT")" == *"up to date"* ]]'
"$W/inst/wallpaperd" -update-rollback >/dev/null
check "回滚到 0.1.0" '[[ "$("$W/inst/wallpaperd" -version)" == *"0.1.0"* ]]'
# 篡改：签名不匹配时拒绝
echo tampered >> "$W/feed/linux-amd64/manifest.json"
check "篡改的清单被拒绝" '! "$W/inst/wallpaperd" -update-apply -feed "http://127.0.0.1:$PORT" 2>/dev/null'
check "拒绝后仍是 0.1.0" '[[ "$("$W/inst/wallpaperd" -version)" == *"0.1.0"* ]]'
echo "update e2e: $pass passed, $fail failed"
[ "$fail" = 0 ]
