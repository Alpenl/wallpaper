# 06 视频管线与编码规范

> **本轮实现接续**：当前 macOS 默认是系统 AVFoundation 隔离 helper，无需 ffmpeg；可选 ffmpeg 分别走 mac VideoToolbox、Win Media Foundation，严格验证 H.264/30fps/无音轨/目标边界。avconvert 只作有限回退。共享保比例产物之外，独立显示器有效 fill/静音时已接入按 backing 像素的惰性 center-cover rendition 缓存；跨屏/声音/非 fill 使用共享回退；原生运行和零拷贝/硬解/性能仍须真机验证。见 [转换契约](../../internal/transcode/README.md)、[研究补充](../research/NATIVE_MVP_ADDENDUM.md)。

> **设计/研究基线**：正文保留原轮次的证据与目标，不表示本轮产品已实现或通过验收。当前状态见 [实现矩阵](../IMPLEMENTATION_STATUS.md)，来源解释与历史差异见 [证据约定](../EVIDENCE.md)。

## 6.1 播放路径
| 平台 | 路径 | 零拷贝 | 依据 |
|---|---|---|---|
| Windows | mpv 子进程，`--wid=<子HWND> --vo=gpu-next --gpu-api=d3d11 --hwdec=d3d11va` | 不用 `*-copy` 时，解码纹理直接给渲染器用 | 【文档】mpv manual `--hwdec`；Lively 用 `auto-safe`【源码】`VideoMpvPlayer.cs`。**实测项 W-T3** |
| Windows 备选 | Media Foundation（Wallpaper Engine 用的就是它；Lively 也有 WMF 播放器） | 由系统决定 | 【文档】WE 帮助页；【源码】Lively `VideoWmfProcess.cs` |
| macOS | AVQueuePlayer + AVPlayerLooper + AVPlayerLayer | 由系统合成 | 【文档】Apple；【源码】phonto |
| Linux | mpv（vaapi/nvdec），Wayland 上用 mpvpaper | | 【源码】Fresco `config.rs:248` |

## 6.2 本 box 软解基线【实测】
- 环境：Intel Xeon（虚拟机，8 vCPU），**没有 GPU**，ffmpeg 7.1.5；脚本见 [bench/sw_decode_bench.sh](bench/sw_decode_bench.sh)。
- 测试素材：10 s、300 帧、30fps 的 testsrc2 加噪声合成画面。噪声让实际码率高于设定值：4K H.264 约 37.6 Mbps，4K HEVC 约 15.2 Mbps，4K AV1 约 8.2 Mbps，1080p H.264 约 11.7 Mbps。**合成画面和真实壁纸素材不同，下表只能用来判断量级。**
- 指标：每 1 秒视频消耗的 CPU 秒数 = (utime+stime)/10。

| 素材 | 单线程 CPU·s / 视频秒 | 自动线程 CPU·s / 视频秒 | 自动线程峰值 RSS |
|---|---|---|---|
| 1080p30 H.264 | 0.146 | 0.158 | 162 MiB |
| 4K30 H.264 | 0.475 | **0.511** | 473 MiB |
| 4K30 HEVC | 0.622 | **0.698** | 387 MiB |
| 4K30 AV1（dav1d） | 0.336 | 0.472 | 305 MiB |
| 4K30 H.264 解码后再全分辨率转 BGRA（模拟回读加 CPU 处理） | — | 0.607 | 613 MiB |
| 4K30 H.264 解码后用 CPU 缩放到 1080p BGRA | — | 0.721 | 521 MiB |

结论：
- 4K30 软解要持续占用大约**半个到 0.7 个 CPU 核**。
- 一旦回读到内存再做 CPU 处理（转色彩空间或缩放），**还要多出 19–41%**。
- 这些都是“没有硬解或走 copy 路径”时的下限代价，也说明为什么零拷贝必须保证。
- 同样的结论在 OBS 的数据里也能看到（PR #6021，3950X：4K H.264 软解 9% CPU，硬解 2%）【实测，第三方】。

## 6.3 GPU 侧开销（第三方实测）
- Fresco 在 Intel N150 + VA-API 上播放 4K60：缩放质量从全质量降到 Reduced，GPU 功耗从 2.77 W 降到 1.60 W；降到 Minimum 是 0.99 W。**解码之外，GPU 缩放是主要的可控开销。**【实测】Fresco README

## 6.4 导入转码规范（v0 草案，【推断】，参数需要用实测 W-T3/M3 校准）
| 项 | 规则 | 理由 |
|---|---|---|
| 分辨率 | 转成**目标显示器的物理分辨率**（按 cover 裁剪），每块显示器各出一份，或者多块同分辨率的屏幕共用一份 | 播放时不用缩放，正好消掉 6.3 节里的缩放开销 |
| 帧率 | 默认 30fps；“流畅”档 60；“省电”档 24 | 解码和合成的负载大致和帧率成正比【推断】 |
| 编码 | Windows：H.264 High（兼容性最好），可选 HEVC Main（需要系统装 HEVC 扩展或用 mpv 自带解码器）；macOS：HEVC Main，H.264 兜底；AV1 只在探测到硬件支持时使用（见 6.7 节的支持表） | WE 官方也推荐 mp4+H.264【文档】 |
| 码率 | 先用 CRF 控制质量（x264 CRF 20–23、x265 CRF 24–26 作为起点），加上 VBV 上限：1080p ≤ 8 Mbps，1440p ≤ 14 Mbps，4K ≤ 25 Mbps（**这些上限是经验起点，要用 VMAF 和功耗实测来校准**） | 码率越高，解码器带宽和功耗越高 |
| GOP | 关键帧间隔 = 帧率 × 2；首帧必须是 IDR；不要 B 帧金字塔太深（`bf=2`） | 循环跳回开头和暂停后恢复时 seek 更快 |
| 音轨 | **去掉** | 省掉音频解码；AVPlayerLooper 要无缝，音轨、视频轨和文件时长必须一致【文档】SO 5361145 里 Apple 工程师的回复 |
| 循环点 | 首尾帧过渡尽量平滑；导入时自动检测首尾帧差异，必要时做 0.5 s 交叉淡化 | WE 官方承认 MF 在循环点会卡，建议把循环点放在画面静止处【文档】 |
| 色彩 | SDR BT.709 8bit，标好 `colorprim/transfer/colormatrix`；HDR 内容另外处理（09 章） | |
| 示例命令 | `ffmpeg -i in -an -vf "scale=W:H:force_original_aspect_ratio=increase,crop=W:H,fps=30" -c:v libx264 -profile:v high -preset slow -crf 21 -maxrate 25M -bufsize 50M -g 60 -bf 2 -pix_fmt yuv420p -color_primaries bt709 -color_trc bt709 -colorspace bt709 -movflags +faststart out.mp4` | 注意：**libx264 是 GPL**，打进产品里分发会违反 LGPL 方案（FFmpeg legal 第 18 条）。产品内转码应该改用**系统编码器**：Windows MF 的 H.264/HEVC 编码器、macOS 的 VideoToolbox（`h264_videotoolbox`/`hevc_videotoolbox`）。见 11 章 |

## 6.5 播放参数（Windows mpv，v0）
`--no-config --idle=yes --wid=<HWND> --input-ipc-server=\\.\pipe\wpd-…-mpv-<display> --log-file=<logs>\mpv-<display>.log --msg-level=all=warn --hwdec=d3d11va --vo=gpu-next --gpu-api=d3d11 --loop-file=inf --keep-open --no-audio --scale=bilinear --dscale=bilinear --cscale=bilinear --no-deband --interpolation=no`
- `--no-config`：避免读取用户自己的 mpv 配置，也避免 MSIX 的路径重定向问题（16 章）；`--idle=yes`：L3 执行 `stop` 后进程保持存活（07 章）；日志用于健康检查（02 章 2.4）【文档】mpv manual。
- 其中 `video-sync` 的取值**未定**：壁纸没有音频，用默认的 `audio` 模式时时钟以系统时钟为准【推断】。具体选哪个，用实测 W-T3 的丢帧统计来决定。

## 6.6 macOS 上的 VP9/WebM 导入路径
1. 解复用：用 libavformat（LGPL，动态链接；11 章）或一个 BSD 许可的 WebM 解复用器。**选哪个还没定**（Q-M4）。
2. 解码：先调用 `VTRegisterSupplementalVideoDecoderIfAvailable(kCMVideoCodecType_VP9)`，再交给 VTDecompressionSession【文档】。
3. 编码：用 VideoToolbox 编成 HEVC（或 H.264）的 mp4，写进 `cache/<id>/renditions/`；播放时走 AVPlayer。
4. 整个过程放在转码 helper 进程里做（02 章），MAS 版需要继承沙盒（11 章）。

## 6.7 硬件解码支持表
| 厂商 | H.264 | HEVC | VP9 | AV1 解码 |
|---|---|---|---|---|
| NVIDIA | ✓ | Pascal 及以后 | Pascal 及以后 | Ampere（RTX 30）及以后 |
| Intel | ✓ | ✓ | ✓ | Xe / Arc 及以后 |
| AMD | ✓ | ✓ | ✓ | VCN 3（RDNA2）及以后 |
| Apple | ✓ | ✓ | M1 起（需要 supplemental decoder） | M3 及以后 |

- 依据：NVDEC 应用说明、Intel Arc 介绍文章、AMF wiki、Apple Newsroom、IINA #3241 的检索综合【文档/推断】。具体型号可能有例外（例如部分低端型号），**运行时以探测结果为准**：Windows 读 mpv 的 `hwdec-current`，macOS 用 `VTIsHardwareDecodeSupported`。转码时据此选择编码格式【推断】。
