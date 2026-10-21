package mpv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// FirstFrame 用 mpv 抽第一帧为 PNG（2.5：首帧截图兼作系统壁纸）。gif 同样适用。
// 参数在盒子上实测过（mpv v0.40.0）：--vo=image 输出 00000001.png。
func FirstFrame(mpvPath, src, dstPNG string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dstPNG), ".snap-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, mpvPath, "--no-config", "--no-audio", "--frames=1", "--vo=image",
		"--vo-image-format=png", "--vo-image-outdir="+tmp, "--hwdec=no", "--msg-level=all=error", "--", src)
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mpv snapshot: %v: %s", err, out)
	}
	return os.Rename(filepath.Join(tmp, "00000001.png"), dstPNG)
}
