// Package transcode 是 06 章 6.4 转码规范在 MVP 里的落地：判断导入的素材能否直接播放，
// 不捆绑编码器：严格转码调用用户安装的 ffmpeg，macOS 使用 VideoToolbox，
// Windows 使用 Media Foundation；macOS avconvert 仅作为明确标示能力受限的系统回退。
package transcode

import (
	"fmt"

	"wpapp/internal/media"
)

// Action 是导入时的处理方式。
type Action string

const (
	None      Action = "none"      // 直接播放
	Recommend Action = "recommend" // 能播放，但建议转码（超规格、含音轨或可能软解）
	Required  Action = "required"  // 本平台播放器不能直接播放
)

// Plan 是判断结果。
type Plan struct {
	Action Action
	Notes  []string
}

// Decide 按平台判断。goos 取 runtime.GOOS。
func Decide(in *media.Info, goos string) Plan {
	p := Plan{Action: None}
	if in == nil {
		return Plan{Action: Required, Notes: []string{"无法探测媒体格式"}}
	}
	if in.Kind == "image" {
		return p
	}
	add := func(a Action, n string) {
		if a == Required || (a == Recommend && p.Action == None) {
			p.Action = a
		}
		p.Notes = append(p.Notes, n)
	}
	switch in.Codec {
	case "h264", "hevc":
		if in.Codec == "hevc" && goos == "windows" {
			p.Notes = append(p.Notes, "HEVC 需要系统安装 HEVC 视频扩展才能用 Media Foundation 硬解；mpv 后端可自带解码")
		}
	case "gif":
		if goos == "darwin" {
			add(Required, "AVPlayer 不能播放 GIF")
		} else {
			add(Recommend, "GIF 应转为视频；没有兼容编码器时只能由播放器直接解码")
		}
	case "prores":
		if goos != "darwin" {
			add(Recommend, "ProRes 码率高，在 Windows/Linux 上只能软解")
		}
	default: // av1 / vp9 / webm / other
		if goos == "darwin" {
			add(Required, fmt.Sprintf("AVFoundation 不能直接播放 %s/%s", in.Container, in.Codec))
		} else {
			add(Recommend, fmt.Sprintf("%s/%s 可能走软解，耗电较高", in.Container, in.Codec))
		}
	}
	if in.W > 3840 || in.H > 2160 {
		add(Recommend, fmt.Sprintf("分辨率 %dx%d 超过 4K", in.W, in.H))
	}
	if in.FPS > 30.5 {
		add(Recommend, fmt.Sprintf("帧率 %.0f 超过优化目标 30", in.FPS))
	}
	if in.HasAudio {
		add(Recommend, "含音轨：建议去掉音轨以利无缝循环")
	}
	return p
}
