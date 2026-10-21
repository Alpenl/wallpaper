#!/usr/bin/env bash
# Linux/X11 端到端（Xvfb）：导入 → 首帧截图 → mpv 画进 _NET_WM_WINDOW_TYPE_DESKTOP 窗口 → 画面在动 →
# kill mpv 后退避重启 → 干净退出（state.json cleanShutdown=true）。
# 需要：xvfb-run、mpv、ffmpeg（只用来生成测试片段和截屏，不打包）。
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=${OUT:-/tmp/wpapp-e2e}
rm -rf "$OUT"; mkdir -p "$OUT"
CGO_ENABLED=0 go build -o "$OUT/wallpaperd" ./cmd/wallpaperd
ffmpeg -loglevel error -f lavfi -i testsrc=size=640x360:rate=30 -t 4 -c:v libx264 -pix_fmt yuv420p "$OUT/clip.mp4"
cat > "$OUT/inner.sh" <<'IN'
set -u
OUT=$1
WPAPP_HOME=$OUT/home "$OUT/wallpaperd" -headless -import "$OUT/clip.mp4" -duration 14s -status "$OUT/status.json" 2>"$OUT/run.log" &
P=$!
sleep 4
ffmpeg -loglevel error -f x11grab -video_size 1280x720 -i "$DISPLAY" -frames:v 1 "$OUT/grab1.png"
sleep 1
ffmpeg -loglevel error -f x11grab -video_size 1280x720 -i "$DISPLAY" -frames:v 1 "$OUT/grab2.png"
pkill -f "mpv.*--wid" && echo "killed mpv" > "$OUT/killed.txt"
sleep 4
ffmpeg -loglevel error -f x11grab -video_size 1280x720 -i "$DISPLAY" -frames:v 1 "$OUT/grab3.png"
wait $P
echo "exit=$?" > "$OUT/exit.txt"
IN
xvfb-run -a -s "-screen 0 1280x720x24" bash "$OUT/inner.sh" "$OUT"
python3 - "$OUT" <<'PY'
import json, sys, subprocess, os
out = sys.argv[1]
def stats(p):
    r = subprocess.run(["ffmpeg","-loglevel","info","-i",p,"-vf","signalstats,metadata=print:key=lavfi.signalstats.YMAX","-f","null","-"],capture_output=True,text=True)
    for l in (r.stdout+r.stderr).splitlines():
        if "YMAX" in l: return float(l.split("=")[1])
    return -1
def md5(p): return subprocess.run(["md5sum",p],capture_output=True,text=True).stdout.split()[0]
log = open(os.path.join(out,"run.log")).read()
st = json.load(open(os.path.join(out,"status.json")))
state = json.load(open(os.path.join(out,"home","config","state.json")))
snap = st["Library"][0].get("snapshot","")
checks = {
  "imported_h264": st["Library"][0]["codec"] == "h264",
  "first_frame_png": os.path.exists(snap) and open(snap,"rb").read(4) == b"\x89PNG",
  "grab1_not_black(YMAX)": stats(os.path.join(out,"grab1.png")) > 100,
  "frames_change": md5(os.path.join(out,"grab1.png")) != md5(os.path.join(out,"grab2.png")),
  "mpv_killed": os.path.exists(os.path.join(out,"killed.txt")),
  "renderer_restarted": log.count('"renderer started"') >= 2,
  "grab3_not_black_after_restart": stats(os.path.join(out,"grab3.png")) > 100,
  "clean_shutdown": state.get("cleanShutdown") is True,
  "exit_0": open(os.path.join(out,"exit.txt")).read().strip() == "exit=0",
}
for k,v in checks.items(): print(("PASS " if v else "FAIL ")+k)
sys.exit(0 if all(checks.values()) else 1)
PY
