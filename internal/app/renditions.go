package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"wpapp/internal/config"
	"wpapp/internal/render"
	"wpapp/internal/transcode"
	"wpapp/internal/visibility"
)

type renditionJob struct {
	item, src  string
	rendition  transcode.Rendition
	generation uint64
	state      string
	explicit   bool
}

type RenditionView struct {
	ItemID, DisplayID, Key, Status string
	Width, Height                  int
}

func (a *App) renditionSourceKey(it *config.Item) string {
	if it == nil || it.Type != "video" {
		return ""
	}
	src := filepath.Join(it.Dir, filepath.FromSlash(it.Entry))
	a.renditionDiskChecks.Add(1)
	st, err := os.Stat(src)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", it.ID, it.Entry, st.Size(), st.ModTime().UnixNano())
}

func (a *App) renditionTargetFor(s *screen, source string) (transcode.Rendition, bool) {
	if s.item == nil || s.item.Type != "video" || !s.canvas.Empty() || source == "" || !managedChild(a.d.Store.Paths.Cache, filepath.Join(a.d.Store.Paths.Cache, s.item.ID)) {
		return transcode.Rendition{}, false
	}
	pb := a.effectivePlayback(s)
	if s.r != nil {
		opts, _ := a.playbackOptions(s, s.r)
		pb.Fit = opts.Fit
	}
	// Cover versions have no audio; preserve an explicit audio preference.
	if pb.Fit != "fill" || !pb.Muted {
		return transcode.Rendition{}, false
	}
	size := s.d.PixelSize()
	if size.X < 2 || size.Y < 2 {
		return transcode.Rendition{}, false
	}
	return transcode.CoverRendition(source, size.X, size.Y), true
}

func (a *App) renditionFor(s *screen) (transcode.Rendition, bool) {
	if s.item == nil || s.item.Type != "video" || !s.canvas.Empty() || !managedChild(a.d.Store.Paths.Cache, filepath.Join(a.d.Store.Paths.Cache, s.item.ID)) {
		return transcode.Rendition{}, false
	}
	source := a.renditionSourceKey(s.item)
	if a.itemSourceKey == nil {
		a.itemSourceKey = map[string]string{}
	}
	a.itemSourceKey[s.item.ID] = source
	return a.renditionTargetFor(s, source)
}

// renditionForItem evaluates the rendition target for (item, screen) using the
// same active-renderer capability rules across View, OptimizeDisplays, and cache refresh.
func (a *App) renditionForItem(s *screen, it *config.Item, useCache bool) (transcode.Rendition, bool) {
	cp := *s
	cp.item = it
	if s.item == nil || s.item.ID != it.ID {
		cp.r = nil
	}
	var source string
	if useCache {
		source = a.itemSourceKey[it.ID]
	} else {
		source = a.renditionSourceKey(it)
		if a.itemSourceKey == nil {
			a.itemSourceKey = map[string]string{}
		}
		a.itemSourceKey[it.ID] = source
	}
	return a.renditionTargetFor(&cp, source)
}

func (a *App) markRenditionReady(itemID, key string, ready bool) {
	if itemID == "" || key == "" {
		return
	}
	if a.renditionReady == nil {
		a.renditionReady = map[string]map[string]bool{}
	}
	if a.renditionReady[itemID] == nil {
		a.renditionReady[itemID] = map[string]bool{}
	}
	a.renditionReady[itemID][key] = ready
}

func (a *App) evictItemRenditions(itemID string) {
	delete(a.itemSourceKey, itemID)
	delete(a.renditionReady, itemID)
}

func (a *App) checkRenditionReadyOnDisk(r transcode.Rendition, itemID string) bool {
	a.renditionDiskChecks.Add(1)
	ready := r.Ready(a.d.Store.Paths.Cache, itemID)
	a.markRenditionReady(itemID, r.Key(), ready)
	return ready
}

func (a *App) refreshItemRenditions(itemID string) {
	it := a.d.Store.Library.Find(itemID)
	if it == nil || it.Type != "video" {
		a.evictItemRenditions(itemID)
		return
	}
	source := a.renditionSourceKey(it)
	if a.itemSourceKey == nil {
		a.itemSourceKey = map[string]string{}
	}
	a.itemSourceKey[itemID] = source
	readyMap := map[string]bool{}
	if source != "" {
		for _, s := range a.sortedScreens() {
			cp := *s
			cp.item = it
			if s.item == nil || s.item.ID != it.ID {
				cp.r = nil
			}
			if r, ok := a.renditionTargetFor(&cp, source); ok {
				key := r.Key()
				if _, seen := readyMap[key]; !seen {
					a.renditionDiskChecks.Add(1)
					readyMap[key] = r.Ready(a.d.Store.Paths.Cache, itemID)
				}
			}
		}
	}
	if a.renditionReady == nil {
		a.renditionReady = map[string]map[string]bool{}
	}
	a.renditionReady[itemID] = readyMap
}

func (a *App) refreshScreenRenditions(s *screen) {
	if s == nil || s.item == nil || s.item.Type != "video" {
		return
	}
	it := a.d.Store.Library.Find(s.item.ID)
	if it == nil {
		return
	}
	source, ok := a.itemSourceKey[it.ID]
	if !ok {
		source = a.renditionSourceKey(it)
		if a.itemSourceKey == nil {
			a.itemSourceKey = map[string]string{}
		}
		a.itemSourceKey[it.ID] = source
	}
	if r, ok := a.renditionTargetFor(s, source); ok {
		a.checkRenditionReadyOnDisk(r, it.ID)
	}
}

func (a *App) refreshRenditionCache() {
	a.itemSourceKey = map[string]string{}
	a.renditionReady = map[string]map[string]bool{}
	screens := a.sortedScreens()
	for i := range a.d.Store.Library.Items {
		it := &a.d.Store.Library.Items[i]
		if it.Type != "video" {
			continue
		}
		source := a.renditionSourceKey(it)
		a.itemSourceKey[it.ID] = source
		if source == "" {
			continue
		}
		readyMap := map[string]bool{}
		for _, s := range screens {
			cp := *s
			cp.item = it
			if s.item == nil || s.item.ID != it.ID {
				cp.r = nil
			}
			if r, ok := a.renditionTargetFor(&cp, source); ok {
				key := r.Key()
				if _, seen := readyMap[key]; !seen {
					a.renditionDiskChecks.Add(1)
					readyMap[key] = r.Ready(a.d.Store.Paths.Cache, it.ID)
				}
			}
		}
		a.renditionReady[it.ID] = readyMap
	}
}

func (a *App) renditionPath(s *screen, queue bool) string {
	fallback := filepath.Join(s.item.Dir, filepath.FromSlash(s.item.Entry))
	r, ok := a.renditionFor(s)
	if !ok {
		return fallback
	}
	if a.checkRenditionReadyOnDisk(r, s.item.ID) {
		return r.Path(a.d.Store.Paths.Cache, s.item.ID)
	}
	if queue {
		a.queueRendition(s.item, r, false)
	}
	return fallback
}

func (a *App) queueRendition(it *config.Item, r transcode.Rendition, retry bool) {
	if a.d.EncoderInfo != nil && !a.encoderInfo.Available {
		if retry {
			a.lastErr = "需要 ffmpeg 才能转码"
		}
		return
	}
	if a.d.EncodeRendition == nil || a.cleaning {
		return
	}
	if a.renditionJobs == nil {
		a.renditionJobs = map[string]*renditionJob{}
	}
	key := r.Key()
	if j := a.renditionJobs[key]; j != nil && ((!retry && j.state != "已优化") || j.state == "等待转码…" || j.state == "正在转码…") {
		return
	}
	if len(a.renditionQueue)+len(a.transcodeQueue) >= MaxQueuedJobs {
		return
	}
	j := &renditionJob{it.ID, filepath.Join(it.Dir, filepath.FromSlash(it.Entry)), r, a.itemGeneration[it.ID], "等待转码…", retry}
	a.renditionJobs[key] = j
	a.renditionQueue = append(a.renditionQueue, j)
	a.startNextTranscode()
}

// OptimizeDisplays retries missing renditions for the item's effective settings
// on each connected display. Matching targets are deduplicated by key.
func (a *App) OptimizeDisplays(id string) {
	a.Do(func() {
		it := a.d.Store.Library.Find(id)
		if it == nil {
			return
		}
		for _, s := range a.sortedScreens() {
			if r, ok := a.renditionForItem(s, it, false); ok && !a.checkRenditionReadyOnDisk(r, id) {
				a.queueRendition(it, r, true)
			}
		}
	})
}

func (a *App) CancelRenditions(id string) {
	a.Do(func() {
		for _, j := range a.renditionJobs {
			if j.item == id && (j.state == "等待转码…" || j.state == "正在转码…") {
				j.state = "已取消"
			}
		}
		if a.activeRendition != nil && a.activeRendition.item == id && a.transcodeCancel != nil {
			a.transcodeCancel()
		}
	})
}

// Called only by the existing encoder queue after its common exclusion gate.
func (a *App) startNextRendition() {
	for len(a.renditionQueue) > 0 {
		j := a.renditionQueue[0]
		a.renditionQueue = a.renditionQueue[1:]
		if j.state != "等待转码…" || a.d.Store.Library.Find(j.item) == nil || j.generation != a.itemGeneration[j.item] {
			continue
		}
		a.activeRendition = j
		a.transcodingID = j.item
		j.state = "正在转码…"
		ctx, cancel := context.WithCancel(a.ctx)
		a.transcodeCancel = cancel
		released := make(chan struct{})
		a.transcodeDone = released
		encode, probe := a.d.EncodeRendition, a.d.ProbeMedia
		cache := a.d.Store.Paths.Cache
		// App owns rendition work; it exits after encoding/cancellation and publication or shutdown cleanup.
		go func() {
			// Staging lives on the cache filesystem so publication is an atomic rename.
			err := os.MkdirAll(cache, 0700)
			tmp := ""
			if err == nil {
				var f *os.File
				f, err = os.CreateTemp(cache, ".rendition-*.mp4")
				if err == nil {
					tmp = f.Name()
					err = f.Close()
				}
			}
			if err == nil {
				err = encode(ctx, j.src, tmp, j.rendition.Target)
			}
			if err == nil {
				if probe == nil {
					err = fmt.Errorf("isolated output probe unavailable")
				} else {
					info, e := probe(ctx, tmp)
					err = e
					if err == nil {
						err = transcode.VerifyCover(info, j.rendition.Target)
					}
				}
			}
			if err == nil {
				err = ctx.Err()
			}
			// Match plain transcodes: deletion waits until encoder and probe release handles.
			close(released)
			done := make(chan struct{})
			a.Do(func() {
				defer close(done)
				defer cancel()
				defer a.advanceJobs()
				a.transcodingID = ""
				a.transcodeCancel = nil
				a.activeRendition = nil
				if j.generation != a.itemGeneration[j.item] || a.d.Store.Library.Find(j.item) == nil {
					return
				}
				if j.state == "已取消" || ctx.Err() != nil {
					j.state = "已取消"
					return
				}
				if err == nil {
					// Reject replacement/modification of the source while encoding.
					it := a.d.Store.Library.Find(j.item)
					st, e := os.Stat(j.src)
					if e != nil || fmt.Sprintf("%s\x00%s\x00%d\x00%d", it.ID, it.Entry, st.Size(), st.ModTime().UnixNano()) != j.rendition.Source {
						err = fmt.Errorf("source changed during encoding")
					}
				}
				if err == nil {
					err = j.rendition.Publish(cache, j.item, tmp)
				}
				if err != nil {
					if errors.Is(err, transcode.ErrNoEncoder) {
						j.state = "需要 ffmpeg 才能转码"
						if j.explicit {
							a.lastErr = j.state
						}
					} else {
						j.state = "优化失败"
						a.lastErr = err.Error()
					}
					return
				}
				j.state = "已优化"
				a.markRenditionReady(j.item, j.rendition.Key(), true)
				a.refreshStorage()
				for _, s := range a.sortedScreens() {
					if s.item != nil && s.item.ID == j.item {
						if m := a.ctl.Display(s.n); m != nil && m.Level() >= visibility.L4 {
							a.setScreenL4Stopped(s)
							continue
						}
						if !a.updateScreenPlayback(s) {
							a.restartPlayback(s)
						}
					}
				}
			})
			select {
			case <-done:
			case <-a.stopped:
			}
			if tmp != "" {
				_ = os.Remove(tmp)
			}
		}()
		return
	}
}

// SourceUpdater is optional: an engine may replace media while preserving its
// timeline and pause state. Engines without it use the existing restart path.
func (a *App) updateRendition(s *screen) bool {
	path := a.renditionPath(s, true)
	if path == s.mediaPath {
		return true
	}
	if !s.started {
		a.capturePosition(s)
		return false
	}
	updater, ok := s.r.(render.SourceUpdater)
	if !ok {
		a.capturePosition(s)
		return false
	}
	gen := s.gen
	it := render.Item{ID: s.item.ID, Kind: s.item.Type, Path: path, Start: s.r.Stats().Pos}
	level := a.ctl.Display(s.n).Level()
	s.ops.enqueue(func() {
		err := updater.UpdateSource(it, level)
		a.Do(func() {
			if s.gen != gen {
				return
			}
			if err != nil {
				a.restartPlayback(s)
			} else {
				s.mediaPath = path
			}
		})
	})
	return true
}

func (a *App) renditionViews() []RenditionView {
	var out []RenditionView
	screens := a.sortedScreens()
	for i := range a.d.Store.Library.Items {
		it := &a.d.Store.Library.Items[i]
		if it.Type != "video" {
			continue
		}
		for _, s := range screens {
			r, ok := a.renditionForItem(s, it, true)
			state := "使用共享版本"
			if ok {
				state = "尚未优化"
				if a.renditionReady[it.ID][r.Key()] {
					state = "已优化"
				} else if j := a.renditionJobs[r.Key()]; j != nil {
					state = j.state
				}
			}
			out = append(out, RenditionView{it.ID, string(s.d.ID), r.Key(), state, r.Target.Width, r.Target.Height})
		}
	}
	return out
}

// Pin both desired keys and paths still loaded by a renderer. The cleanup runs
// on the event loop so assignment and publication cannot race the sweep.
func (a *App) cleanRenditions() error {
	keep := map[string]map[string]bool{}
	pin := func(item, key string) {
		if keep[item] == nil {
			keep[item] = map[string]bool{}
		}
		keep[item][key] = true
	}
	for _, s := range a.screens {
		if s.item == nil {
			continue
		}
		if r, ok := a.renditionFor(s); ok {
			pin(s.item.ID, r.Key())
		}
		if s.mediaPath != "" {
			pin(s.item.ID, filepath.Base(filepath.Dir(s.mediaPath)))
		}
	}
	for _, j := range a.renditionJobs {
		if j.state == "等待转码…" || j.state == "正在转码…" {
			pin(j.item, j.rendition.Key())
		}
	}
	for _, it := range a.d.Store.Library.Items {
		if managedChild(a.d.Store.Paths.Cache, filepath.Join(a.d.Store.Paths.Cache, it.ID)) {
			if err := transcode.CleanRenditions(a.d.Store.Paths.Cache, it.ID, keep[it.ID]); err != nil {
				return err
			}
		}
	}
	a.refreshRenditionCache()
	return nil
}
