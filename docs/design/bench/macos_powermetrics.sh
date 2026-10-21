#!/usr/bin/env bash
# 动态壁纸 macOS 采集（草稿，未在真机运行过）。需 sudo。
# 用法: sudo ./macos_powermetrics.sh baseline|wallpaper 120 "wpproto|WallpaperHost"
set -euo pipefail
PHASE=${1:-baseline}; N=${2:-120}; PAT=${3:-wpproto}; TS=$(date +%Y%m%d_%H%M%S); mkdir -p out
RAW=out/pm_${PHASE}_$TS.txt; CSV=out/pm_${PHASE}_$TS.csv
powermetrics --samplers cpu_power,gpu_power,tasks --show-process-gpu -i 1000 -n "$N" > "$RAW"
# 提取：每秒 CPU/GPU/组合功耗(mW)、GPU 活跃度，以及匹配进程的 CPU ms/s 和 GPU ms/s
awk -v pat="$PAT" 'BEGIN{OFS=",";print "sample,cpu_mW,gpu_mW,combined_mW,gpu_active_pct,proc_cpu_ms_s,proc_gpu_ms_s"}
 /^\*\*\*\*\* Sampled system activity/{if(n)print n,c,g,cb,ga,pc,pg; n++;c=g=cb=ga=pc=pg=""}
 /^CPU Power:/{c=$3} /^GPU Power:/{g=$3} /^GPU HW active residency:/{ga=$5+0}
 /^Combined Power/{cb=$NF;sub(/mW/,"",cb);cb=$(NF-1)}
 $0~pat && /^[A-Za-z]/{pc+=$3; pg+=$NF}
 END{print n,c,g,cb,ga,pc,pg}' "$RAW" > "$CSV"
echo "完成: $CSV （tasks 列含义随 macOS 版本变化，首次运行请对照 $RAW 校正 awk 列号）"
