package app

import (
	"fmt"
	"image"
	"path/filepath"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render/panorama"
)

// desiredCanvas is empty for independent mode or an unsupported host/topology.
// Persisted spanning preferences survive a temporary unsupported connection.
func (a *App) desiredCanvas() (image.Rectangle, string) {
	if a.d.Store.Assign.Layout != "span" {
		return image.Rectangle{}, ""
	}
	if !a.d.Host.Caps().Spanning {
		return image.Rectangle{}, "当前桌面宿主不支持跨屏画布，暂时使用独立显示器"
	}
	if _, ok := a.d.Host.(desktop.CanvasHost); !ok {
		return image.Rectangle{}, "当前桌面宿主没有跨屏画布接口，暂时使用独立显示器"
	}
	canvas, err := desktop.CanvasBounds(a.d.Host.Displays())
	if err != nil {
		return image.Rectangle{}, "跨屏画布暂不可用：" + err.Error()
	}
	return canvas, ""
}
func (a *App) spanning() bool { canvas, _ := a.desiredCanvas(); return !canvas.Empty() }
func (a *App) createSurface(s *screen) (desktop.Surface, error) {
	if !s.canvas.Empty() {
		h, ok := a.d.Host.(desktop.CanvasHost)
		if !ok {
			return nil, fmt.Errorf("desktop: spanning unsupported")
		}
		return h.CreateCanvasSurface(s.d.ID, s.canvas)
	}
	return a.d.Host.CreateSurface(s.d.ID)
}
func (a *App) assignmentFor(s *screen) string {
	if !s.canvas.Empty() {
		return a.d.Store.Assign.SpanItem
	}
	return a.d.Store.Assign.For(string(s.d.ID))
}
func (a *App) displayOptions(s *screen) config.DisplayOptions {
	o := a.d.Store.Assign.OptionsFor(string(s.d.ID))
	if !s.canvas.Empty() {
		span := a.d.Store.Assign.SpanOptions
		span.Normalize()
		o.Fit, o.Speed = span.Fit, span.Speed
	}
	return o
}

// effectivePlayback resolves display overrides over the wallpaper group's own props.
func (a *App) effectivePlayback(s *screen) config.Playback {
	var props config.ItemProps
	if s.item != nil {
		props = s.item.Props
	}
	return config.ResolvePlayback(props, a.displayOptions(s), a.d.Store.Config.Render.Muted)
}

// SetLayout preserves the independent assignments/options for returning later.
// A panorama uses one item and fit/speed, while pause/profile remain per output.
func (a *App) SetLayout(layout, itemID string) {
	a.Do(func() {
		if layout != "independent" && layout != "span" {
			a.lastErr = "未知显示器布局"
			return
		}
		if layout == "span" {
			if !a.d.Host.Caps().Spanning {
				a.lastErr = "当前桌面宿主不支持跨屏画布"
				return
			}
			if _, ok := a.d.Host.(desktop.CanvasHost); !ok {
				a.lastErr = "当前桌面宿主不支持跨屏画布"
				return
			}
			if _, err := desktop.CanvasBounds(a.d.Host.Displays()); err != nil {
				a.lastErr = err.Error()
				return
			}
			if itemID == "" {
				itemID = a.d.Store.Assign.SpanItem
			}
			if itemID == "" {
				itemID = a.d.Store.Assign.Default
			}
			if itemID != "" && a.d.Store.Library.Find(itemID) == nil {
				a.lastErr = "找不到要应用的壁纸"
				return
			}
			a.d.Store.Assign.SpanItem = itemID
			a.d.Store.Assign.SpanOptions.Normalize()
		}
		a.d.Store.Assign.Layout = layout
		a.saveAssign()
		a.syncDisplays()
		if layout == "span" {
			a.assign("", itemID)
		}
	})
}

// System wallpaper fallback follows the same canvas mapping as video. Cropping
// and file I/O run on the display's serialized worker, before that renderer loads.
func (a *App) installSystemWallpaper(s *screen) {
	if s.item == nil {
		return
	}
	source := s.item.Snapshot
	if s.item.Type == "image" {
		source = filepath.Join(s.item.Dir, filepath.FromSlash(s.item.Entry))
	}
	if source == "" {
		return
	}
	host, id, itemID := a.d.Host, s.d.ID, s.item.ID
	canvas, viewport, pixels, fit := s.canvas, s.d.Bounds, s.d.PixelSize(), a.displayOptions(s).Fit
	cache := filepath.Join(a.d.Store.Paths.Cache, itemID)
	s.ops <- func() {
		select {
		case <-s.quit:
			return
		default:
		}
		path := source
		var err error
		if !canvas.Empty() {
			path, err = panorama.CropSized(source, cache, canvas, viewport, pixels, fit)
		}
		if err == nil {
			select {
			case <-s.quit:
				return
			default:
			}
			err = host.SetSystemWallpaper(id, path)
		}
		if err != nil {
			a.log.Warn("set system wallpaper", "display", id, "err", err)
			// Surface rendering may still work, but a failed static/fallback crop must
			// not be misrepresented as successful spanning.
			if !canvas.Empty() {
				a.Do(func() {
					if a.screens[id] == s && s.canvas == canvas && s.item != nil && s.item.ID == itemID {
						a.lastErr = "跨屏静态壁纸或暂停画面失败：" + err.Error()
					}
				})
			}
		}
	}
}

// One display owns shared resume history and optional audio. Physical display
// histories remain untouched, so leaving span restores independent playback.
func topologyOwner(displays map[desktop.DisplayID]desktop.Display) desktop.DisplayID {
	ordered := layoutOrder(displays)
	for _, d := range ordered {
		if d.Primary {
			return d.ID
		}
	}
	if len(ordered) > 0 {
		return ordered[0].ID
	}
	return ""
}

func (a *App) spanLeader() *screen {
	if a.spanOwner != "" {
		return a.screens[a.spanOwner]
	}
	screens := a.layoutScreens()
	for _, s := range screens {
		if s.d.Primary {
			return s
		}
	}
	if len(screens) > 0 {
		return screens[0]
	}
	return nil
}
func (a *App) positionKey(s *screen) string {
	if !s.canvas.Empty() {
		return "@span|" + s.item.ID
	}
	return string(s.d.ID) + "|" + s.item.ID
}
