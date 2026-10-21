// Package image 是静态图片壁纸（F-08）：不启动任何渲染进程，把图片设为系统壁纸并隐藏我们的表面，CPU 约为 0。
package image

import (
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

// Renderer 实现 render.Renderer。
type Renderer struct {
	s    desktop.Surface
	done chan error
}

// New 创建静态图渲染器。
func New() *Renderer { return &Renderer{done: make(chan error)} }

func (r *Renderer) Load(it render.Item, s desktop.Surface, _ bool) error {
	r.s = s
	return s.SetVisible(false) // 画面由系统壁纸（app 在加载前已设置）提供
}
func (r *Renderer) SetLevel(visibility.Level) error { return nil }
func (r *Renderer) SetFPSCap(int) error             { return nil }
func (r *Renderer) Snapshot(string) error           { return render.ErrUnsupported }
func (r *Renderer) Stats() render.Stats             { return render.Stats{Backend: "image"} }
func (r *Renderer) Done() <-chan error              { return r.done }
func (r *Renderer) Close() error                    { return nil }

var _ render.Renderer = (*Renderer)(nil)
