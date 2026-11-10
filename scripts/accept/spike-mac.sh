#!/usr/bin/env bash
# Run on the real desktop, with other windows minimized and a visibly moving clip.
# DURATION defaults to 30 minutes; shorter runs retain BLOCKED long-run items.
set -euo pipefail
cd "$(dirname "$0")/../.."
video=${1:?Usage: scripts/accept/spike-mac.sh video}
video=$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$video")
output=${OUTPUT:-$(mktemp -d "${TMPDIR:-/tmp}/wallpaper-spike-mac.XXXXXX")}
mkdir -p "$output"
export WPAPP_HOME="$output/isolated-home"
export XDG_RUNTIME_DIR="$output"
exec > >(tee "$output/results.txt") 2>&1
pid=''
failed=0
cleanup() { if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then kill -TERM "$pid" 2>/dev/null || true; fi; }
trap cleanup EXIT
printf 'Artifacts: %s\n' "$output"
git rev-parse HEAD > "$output/commit.txt"
sw_vers > "$output/system.txt"
system_profiler SPHardwareDataType SPDisplaysDataType > "$output/device.txt"
shasum -a 256 "$video" > "$output/media.txt"
cargo build --release --locked -p wallpaper-spike
cp target/release/wallpaper-spike "$output/wallpaper-spike"
"$output/wallpaper-spike" --launch-at-login on
"$output/wallpaper-spike" --launch-at-login status > "$output/login-status.json"
python3 - "$output/login-status.json" <<'PYTEST'
import json,sys
s=json.load(open(sys.argv[1])); assert s['backend']=='isolated' and s['registered'] and s['pathMatches']
PYTEST
"$output/wallpaper-spike" --launch-at-login off
"$output/wallpaper-spike" --export-diagnostics "$output/diagnostics.json"
echo 'PASS isolated launch-at-login and diagnostics smoke'

CGO_ENABLED=0 go build -o "$output/probe" ./scripts/accept/probe
status="$output/status.json"
field() { python3 -c 'import json,sys; s=json.load(open(sys.argv[1])); assert s["schema_version"] == 3; print(s["displays"][0].get(sys.argv[2],""))' "$status" "$1" 2>/dev/null || true; }
wait_confirmed() {
  for ((n=0;n<250;n++)); do
    phase=$(field phase)
    if [ -f "$status" ]; then cp "$status" "$output/startup-$n.json"; fi
    if [ "$phase" = confirmed ]; then return 0; fi
    if [ "$phase" = failed ] || [ "$phase" = stopped ] || ! kill -0 "$pid" 2>/dev/null; then return 1; fi
    sleep .1
  done
  return 1
}
"$output/wallpaper-spike" --play "$video" --status "$status" > "$output/app.log" 2>&1 & pid=$!
if ! wait_confirmed; then echo 'FAIL SPIKE-04 startup did not confirm'; cat "$status" 2>/dev/null || true; exit 1; fi
if [ "$(field pid)" != "$pid" ]; then echo 'FAIL status belongs to another process'; exit 1; fi
echo 'PASS SPIKE-04 confirmed with platform evidence (see status history)'
window=$(field window)
cp "$status" "$output/confirmed.json"
echo 'timestamp,pid,rss_kib' > "$output/rss.csv"
duration=${DURATION:-1800}
capture=true
if ! "$output/probe" -permission; then capture=false; echo 'BLOCKED SPIKE-02 desktop capture permission'; fi
start=$SECONDS
index=0
motion_failed=false
while (( SECONDS-start < duration )); do
  if ! kill -0 "$pid" 2>/dev/null || [ "$(field phase)" != confirmed ]; then echo 'FAIL SPIKE-02 playback stopped or evidence lost'; exit 1; fi
  cp "$status" "$output/status-$index.json"
  printf '%s,%s,%s\n' "$(date -u +%FT%TZ)" "$pid" "$(ps -o rss= -p "$pid" | tr -d ' ')" >> "$output/rss.csv"
  if $capture; then
    image="$output/frame-$index.png"
    if ! screencapture -x -D 1 "$image" || [ ! -s "$image" ]; then
      echo 'BLOCKED SPIKE-02 screencapture could not capture desktop'; capture=false
    elif ((index>0)); then
      if ! "$output/probe" "$output/frame-$((index-1)).png" "$image" > "$output/diff-$index.txt" 2>&1; then
        cat "$output/diff-$index.txt"
        if grep -q BLOCKED "$output/diff-$index.txt"; then capture=false; echo 'BLOCKED SPIKE-02 missing desktop pixels'; else motion_failed=true; failed=1; echo 'FAIL SPIKE-02 screenshot did not change'; fi
      fi
    fi
  fi
  index=$((index+1))
  sleep 10 # Real-device sampling interval; not a unit-test scheduling guess.
done
if $capture && ! $motion_failed && ((duration>=600)); then echo 'PASS SPIKE-02 ten-minute desktop motion samples'; elif ((duration<600)); then echo 'BLOCKED SPIKE-02 shortened observation'; fi
# Keep raw resource evidence; growth needs comparison across the observation.
python3 - "$output/rss.csv" "$duration" <<'PY' || failed=1
import csv,sys,statistics
rows=list(csv.DictReader(open(sys.argv[1])));v=[int(r['rss_kib']) for r in rows]
if int(sys.argv[2])<1800 or len(v)<10:
 print('BLOCKED SPIKE-08 requires 30 minutes');sys.exit()
k=max(1,len(v)//5);a=statistics.median(v[:k]);b=statistics.median(v[-k:])
growing=b-a>max(50*1024,a*.2)
print(('FAIL' if growing else 'PASS')+f' SPIKE-08 RSS sampled trend: first={a} KiB last={b} KiB (50 MiB/20% threshold; raw CSV retained)')
sys.exit(1 if growing else 0)
PY
children=$(pgrep -P "$pid" || true)
exit_start=$SECONDS
kill -TERM "$pid"
for ((n=0;n<50;n++)); do if ! kill -0 "$pid" 2>/dev/null; then break; fi; sleep .1; done
if kill -0 "$pid" 2>/dev/null; then echo 'FAIL SPIKE-07 exit exceeded 5 seconds'; exit 1; fi
wait "$pid" || { echo 'FAIL SPIKE-07 abnormal exit'; exit 1; }
for child in $children; do if kill -0 "$child" 2>/dev/null; then echo "FAIL SPIKE-07 residual child $child"; exit 1; fi; done
"$output/probe" -window "$window" || { echo 'FAIL SPIKE-07 residual native surface'; exit 1; }
if [ "$(field phase)" != stopped ]; then echo 'FAIL SPIKE-07 missing stopped snapshot'; exit 1; fi
echo "PASS SPIKE-07 SIGTERM exit, no child or surface residue ($((SECONDS-exit_start)) s)"
pid=''
# Corrupt media must fail and exit, preserving its reason in the final snapshot.
printf 'invalid media\n' > "$output/corrupt.mp4"
rm -f "$status"
"$output/wallpaper-spike" --play "$output/corrupt.mp4" --status "$status" > "$output/corrupt.log" 2>&1 & pid=$!
for ((n=0;n<250;n++)); do if ! kill -0 "$pid" 2>/dev/null; then break; fi; sleep .1; done
if kill -0 "$pid" 2>/dev/null; then echo 'FAIL SPIKE-05 corrupt media process remained'; exit 1; fi
result=0;wait "$pid" || result=$?
if [ "$result" = 1 ] && [ "$(field phase)" = failed ] && [ -n "$(field reason)" ] && [ "$(field visible)" = no ]; then echo 'PASS SPIKE-05 corrupt media rejected and cleaned'; else failed=1; echo 'FAIL SPIKE-05 missing failure/cleanup evidence'; fi
pid=''
echo 'BLOCKED SPIKE-01/SPIKE-03 require recording review for first-frame flicker and loop seams'
echo 'BLOCKED SPIKE-06/SPIKE-07 desktop icon interaction and restored desktop require human confirmation'
echo 'BLOCKED SPIKE-TRAY close/reopen settings, pause/resume and tray exit require human interaction'
echo 'BLOCKED SPIKE-LOCK lock/unlock recovery requires human observation'
echo 'BLOCKED SPIKE-DOCK verify no Dock icon and settings activation on the real desktop'
echo "Artifacts: $output"

exit "$failed"
