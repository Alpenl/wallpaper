package app

import (
	"errors"
	"math"
	"strings"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

// SetDisplayOptions changes playback and pause policy for one stable display ID.
// Options remain associated with that display across reconnects and assignments.
func (a *App) SetDisplayOptions(display string, options config.DisplayOptions) {
	a.Do(func() {
		if display == "" {
			return
		}
		options.Normalize()
		old := a.d.Store.Assign.OptionsFor(display)
		span := a.spanning()
		sharedChanged := false
		if span {
			shared := a.d.Store.Assign.SpanOptions
			shared.Normalize()
			sharedChanged = shared.Fit != options.Fit || shared.Speed != options.Speed
			shared.Fit, shared.Speed = options.Fit, options.Speed
			a.d.Store.Assign.SpanOptions = shared
			options.Fit, options.Speed = old.Fit, old.Speed
		}
		if a.d.Store.Assign.Options == nil {
			a.d.Store.Assign.Options = map[string]config.DisplayOptions{}
		}
		a.d.Store.Assign.Options[display] = options
		a.saveAssign()
		id := desktop.DisplayID(display)
		if options.Paused {
			a.perDisp[id] |= visibility.User
		} else {
			a.perDisp[id] &^= visibility.User
		}
		s := a.screens[id]
		if s == nil {
			return
		}
		_ = a.ctl.Set(a.now(), visibility.User, s.n, options.Paused)
		if old.Fit != options.Fit || old.Speed != options.Speed || old.Audio != options.Audio {
			if !a.updateScreenPlayback(s) {
				a.restartPlayback(s)
			}
		}
		a.applyCap(s)
		if sharedChanged {
			for _, output := range a.sortedScreens() {
				a.installSystemWallpaper(output)
				if !a.updateScreenPlayback(output) {
					a.restartPlayback(output)
				}
			}
		}
	})
}

// SetItemProps changes a wallpaper group's own playback properties (fit, speed,
// volume, audio). Every screen showing it picks them up unless that display
// overrides the same property.
func (a *App) SetItemProps(itemID string, props config.ItemProps) {
	a.Do(func() {
		it := a.d.Store.Library.Find(itemID)
		if it == nil {
			a.lastErr = "找不到这张壁纸"
			return
		}
		props.Normalize()
		if it.Props == props {
			return
		}
		it.Props = props
		a.saveLibrary()
		for _, s := range a.sortedScreens() {
			if s.item == nil || s.item.ID != itemID {
				continue
			}
			s.item.Props = props
			if !a.updateScreenPlayback(s) {
				a.restartPlayback(s)
			}
		}
	})
}

func (a *App) restartPlayback(s *screen) {
	a.closeScreen(s, false)
	if a.ctl.Display(s.n).Level() < visibility.L4 {
		a.startScreen(s, 0)
	}
}

// Updates are serialized with renderer loads/pauses. Unsupported backends retain
// the explicit restart fallback, while other errors are surfaced without a loop.
func (a *App) updateScreenPlayback(s *screen) bool {
	updater, ok := s.r.(interface {
		UpdatePlayback(render.PlaybackOptions) error
	})
	if !ok || !s.started {
		return false
	}
	gen, r := s.gen, s.r
	options, note := a.playbackOptions(s, r)
	s.ops <- func() {
		err := updater.UpdatePlayback(options)
		a.Do(func() {
			if s.gen != gen {
				return
			}
			switch {
			case errors.Is(err, render.ErrUnsupported):
				a.restartPlayback(s)
			case err != nil:
				s.note = "调整播放设置失败：" + err.Error()
				a.lastErr = s.note
			default:
				s.note = note
			}
		})
	}
	return true
}

func (a *App) playbackOptions(s *screen, r render.Renderer) (render.PlaybackOptions, string) {
	c := a.d.Store.Config
	pb := a.effectivePlayback(s)
	p := render.PlaybackOptions{Fit: pb.Fit, Speed: pb.Speed, Muted: pb.Muted, Volume: pb.Volume, Scale: c.Perf.RenderScale}
	if !s.canvas.Empty() && s != a.spanLeader() {
		p.Muted = true
	}
	caps := render.Capabilities{}
	if cp, ok := r.(render.CapabilityProvider); ok {
		caps = cp.Capabilities()
	}
	var missing []string
	if !caps.Fit {
		if p.Fit != "fill" && !(s.item != nil && s.item.Type == "image" && !s.canvas.Empty()) {
			missing = append(missing, "适配方式")
		}
		p.Fit = "fill"
	}
	if !caps.Speed {
		if p.Speed != 1 {
			missing = append(missing, "播放速度")
		}
		p.Speed = 1
	}
	if !caps.Audio {
		if !p.Muted {
			missing = append(missing, "音频")
		}
		p.Muted = true
	}
	if !caps.Scale {
		if p.Scale != 1 {
			missing = append(missing, "视频降采样")
		}
		p.Scale = 1
	}
	if len(missing) == 0 {
		return p, ""
	}
	return p, "当前后端不支持" + strings.Join(missing, "、") + "，已使用默认值"
}

func (a *App) reasonEnabled(r visibility.Reason) bool {
	switch r {
	case visibility.Fullscreen:
		return a.d.Store.Config.Pause.OnFullscreen && a.foregroundAction != "ignore"
	case visibility.Covered:
		return a.d.Store.Config.Pause.OnCovered && a.foregroundAction != "ignore"
	default:
		return true
	}
}

func (a *App) updateAppRule() {
	next := a.d.Store.Config.Pause.AppAction(a.foreground)
	if next == a.foregroundAction {
		return
	}
	a.foregroundAction = next
	a.global[visibility.FocusedApp] = next == "pause"
	_ = a.ctl.Set(a.now(), visibility.FocusedApp, -1, next == "pause")
	for id, s := range a.screens {
		for _, reason := range []visibility.Reason{visibility.Fullscreen, visibility.Covered} {
			_ = a.ctl.Set(a.now(), reason, s.n, a.perDisp[id]&reason != 0 && a.reasonEnabled(reason))
		}
		a.applyCap(s)
	}
}

func (a *App) capturePosition(s *screen) {
	if !a.d.Store.Config.Render.ResumePosition || s.item == nil || s.r == nil || !s.started {
		return
	}
	if cp, ok := s.r.(render.CapabilityProvider); ok && !cp.Capabilities().Resume {
		return
	}
	if !s.canvas.Empty() && s != a.spanLeader() {
		return
	}
	pos := s.r.Stats().Pos
	if math.IsNaN(pos) || math.IsInf(pos, 0) || pos < 0 {
		return
	}
	a.d.Store.State.Positions[a.positionKey(s)] = pos
}

func (a *App) checkpointPositions() {
	if !a.d.Store.Config.Render.ResumePosition {
		return
	}
	for _, s := range a.screens {
		a.capturePosition(s)
	}
	a.saveState()
}

func cloneView(v View) View {
	v.Config = v.Config.Clone()
	v.Displays = append([]DisplayView(nil), v.Displays...)
	v.Library = append([]config.Item(nil), v.Library...)
	for i := range v.Library {
		v.Library[i].Tags = append([]string(nil), v.Library[i].Tags...)
		v.Library[i].Colors = append([]string(nil), v.Library[i].Colors...)
	}
	v.ReadOnly = append([]string(nil), v.ReadOnly...)
	m := make(map[string]string, len(v.Transcoding))
	for k, val := range v.Transcoding {
		m[k] = val
	}
	v.Transcoding = m
	return v
}
