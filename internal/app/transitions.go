package app

import (
	"log/slog"
	"time"
	"wpapp/internal/render"
	"wpapp/internal/visibility"
)

func (a *App) transitionLog(s *screen, r render.Renderer, gen int, op string) *slog.Logger {
	item, backend := "unknown", "none"
	if s.item != nil {
		item = s.item.ID
	}
	pid := 0
	if r != nil {
		backend = r.Stats().Backend
		if p, ok := r.(interface{ ChildPID() int }); ok {
			pid = p.ChildPID()
		}
	}
	return a.log.With("display", s.d.ID, "item", item, "backend", backend, "child_pid", pid, "generation", gen, "op", op)
}

// closeScreen 结束渲染器；destroySurface=true 时也关闭表面和 worker。
func (a *App) closeScreen(s *screen, destroySurface bool) {
	a.closeScreenForPause(s, destroySurface, false)
}

func (a *App) closeScreenForPause(s *screen, destroySurface, policyPause bool) {
	countStopped := policyPause
	r, surf := s.r, s.surf
	a.capturePosition(s)
	if s.watchStop != nil {
		close(s.watchStop)
		s.watchStop = nil
	}
	s.r, s.started, s.metricsReady = nil, false, false
	s.levelPending = 0
	s.gen++
	gen := s.gen
	if destroySurface {
		s.surf = nil
	}
	id := s.d.ID
	queued := time.Now()
	log := a.transitionLog(s, r, gen, "close")
	done := make(chan struct{})
	s.ops <- func() {
		var closeErr error
		begin := time.Now()
		from := s.worker.level
		ctx := log.With("from", from.String(), "to", "L4")
		ctx.Info("transition_begin")
		if r != nil {
			closeErr = r.Close()
		}
		ctx.Info("transition_end", "queue_ms", begin.Sub(queued).Milliseconds(), "elapsed_ms", time.Since(begin).Milliseconds(), "err", closeErr)
		if policyPause {
			if closeErr == nil {
				s.worker.level = visibility.L4
			}

			if closeErr != nil {
				a.log.Error("renderer stop failed", "display", id, "err", closeErr)
				a.Do(func() {
					if s.gen == gen {
						s.levelNote = "播放状态恢复失败，请重新应用壁纸"
					}
				})
			}
		}
		if countStopped && closeErr == nil {
			a.Do(func() {
				if s.gen == gen {
					s.metricsReady, s.metricsLevel = true, visibility.L4
				}
			})
		}
		if destroySurface && surf != nil {
			_ = surf.Close()
		}
		close(done)
	}
	if destroySurface {
		close(s.quit)
		select { // 有界等待，避免卡住事件循环
		case <-done:
		case <-time.After(3 * time.Second):
			a.log.Warn("close screen timed out", "display", id)
		}
	}
}

func (a *App) applyLevel(s *screen, l visibility.Level) {
	r := s.r
	if r == nil {
		return
	}
	if l >= visibility.L4 {
		a.closeScreenForPause(s, false, true)
		if s.surf != nil {
			surf := s.surf
			s.ops <- func() { _ = surf.SetVisible(false) }
		}
		return
	}
	gen := s.gen
	id := s.d.ID
	queued := time.Now()
	log := a.transitionLog(s, r, gen, "set_level")
	s.levelPending++
	s.ops <- func() {
		begin := time.Now()
		from := s.worker.level
		ctx := log.With("from", from.String(), "to", l.String())
		ctx.Info("transition_begin")
		err := r.SetLevel(l)
		if err == nil {
			s.worker.level = l
		}
		ctx.Info("transition_end", "queue_ms", begin.Sub(queued).Milliseconds(), "elapsed_ms", time.Since(begin).Milliseconds(), "err", err)
		a.Do(func() {
			if s.gen != gen {
				return
			}
			s.levelPending--
			if err != nil {
				a.log.Error("renderer level transition failed", "display", id, "level", l.String(), "err", err)
				s.levelNote = "播放状态切换失败，已尝试重启渲染器"
				if s.levelFailures < 2 {
					s.levelFailures++
					a.restartPlayback(s)
				} else {
					s.levelExhausted = true
					s.levelNote = "播放状态恢复失败，请重新应用壁纸"
					a.closeScreen(s, false)
				}
				return
			}
			if s.started {
				s.metricsReady, s.metricsLevel = true, l
			}
		})
	}

}
