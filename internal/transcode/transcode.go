// Package transcode 是 06 章 6.4 转码规范在 MVP 里的落地：判断导入的素材能否直接播放，
// 需要转码时只调用系统编码器（macOS：/usr/bin/avconvert，基于 AVFoundation/VideoToolbox），
// 不捆绑任何 GPL 编码器（11 章 11.5）。Windows/Linux 上 MVP 没有系统编码器挂钩，只给出提示。
package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

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
	if in.FPS > 60.5 {
		add(Recommend, fmt.Sprintf("帧率 %.0f 超过 60", in.FPS))
	}
	if in.HasAudio {
		p.Notes = append(p.Notes, "含音轨：播放时静音；06 章建议去掉音轨以利无缝循环")
	}
	return p
}

// ErrNoEncoder 表示本平台没有可用的系统编码器挂钩。
var ErrNoEncoder = errors.New("transcode: no system encoder available on this platform in the MVP; please convert to H.264 MP4 first")

// AvconvertPath 是 macOS 自带的 avconvert。
const AvconvertPath = "/usr/bin/avconvert"

// System 返回本平台的系统编码器调用；没有时返回 nil。
func System(goos string) func(ctx context.Context, src, dst string) error {
	if goos != "darwin" {
		return nil
	}
	if _, err := os.Stat(AvconvertPath); err != nil {
		return nil
	}
	return func(ctx context.Context, src, dst string) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		// 未在真机验证：preset 名取自 avconvert --help 的 HEVC 预设，输出 .mov
		cmd := exec.CommandContext(ctx, AvconvertPath, "--preset", "PresetHEVCHighestQuality", "--source", src, "--output", dst, "--replace")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("avconvert: %v: %s", err, out)
		}
		return nil
	}
}
