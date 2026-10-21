#!/usr/bin/env bash
# 软解基线：box 上没有 GPU（无 /dev/dri），只能测 CPU 软解成本，作为“为什么必须硬解”的下限参考。
# 用法：bash sw_decode_bench.sh ；依赖 ffmpeg（含 libx264/libx265/libsvtav1/libdav1d）
set -e; cd /tmp
gen(){ ffmpeg -hide_banner -v error -f lavfi -i "testsrc2=size=$1:rate=30,noise=alls=20:allf=t" -t 10 "${@:3}" -y "$2"; }
gen 1920x1080 s1080_h264.mp4 -c:v libx264 -preset veryfast -b:v 8M -pix_fmt yuv420p
gen 3840x2160 s4k_h264.mp4  -c:v libx264 -preset veryfast -b:v 20M -pix_fmt yuv420p
gen 3840x2160 s4k_hevc.mp4  -c:v libx265 -preset ultrafast -b:v 15M -pix_fmt yuv420p
gen 3840x2160 s4k_av1.mp4   -c:v libsvtav1 -preset 12 -b:v 12M
b(){ ffmpeg -hide_banner -benchmark -v info "$@" -f null - 2>&1 | grep -E "^bench" | tr '\n' ' '; echo; }
for f in s1080_h264 s4k_h264 s4k_hevc s4k_av1; do for t in 1 0; do echo -n "$f threads=$t: "; b -threads $t -i $f.mp4; done; done
echo -n "4k h264 + 全分辨率转 BGRA(模拟回读后 CPU 处理): "; b -i s4k_h264.mp4 -pix_fmt bgra
echo -n "4k h264 + CPU 缩放到 1080p BGRA: "; b -i s4k_h264.mp4 -vf scale=1920:1080 -pix_fmt bgra
