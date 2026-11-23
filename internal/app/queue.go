package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
	"wpapp/internal/importer"
	"wpapp/internal/media"
	"wpapp/internal/telemetry"
	"wpapp/internal/visibility"
)

// ApplyAll 是 Import 的 applyTo 取值：导入后应用到所有显示器（headless / e2e 用）。
const ApplyAll = "*"

// MaxQueuedJobs limits pending import requests and encoder jobs separately.
const MaxQueuedJobs = 256

type importRequest struct{ path, applyTo string }

// trackRetiring records an asynchronous teardown barrier for itemID so that a
// subsequent Remove or BeforeReplace waits even if the screen already switched
// to another wallpaper or was unplugged before teardown finished.
func (a *App) trackRetiring(itemID string, done <-chan struct{}) {
	if itemID == "" || done == nil {
		return
	}
	if a.retiring == nil {
		a.retiring = map[string][]<-chan struct{}{}
	}
	a.pruneRetiring()
	select {
	case <-done:
		return
	default:
	}
	a.retiring[itemID] = append(a.retiring[itemID], done)
}

func (a *App) pruneRetiring() {
	for id, chans := range a.retiring {
		keep := chans[:0]
		for _, ch := range chans {
			select {
			case <-ch:
			default:
				keep = append(keep, ch)
			}
		}
		if len(keep) == 0 {
			delete(a.retiring, id)
		} else {
			a.retiring[id] = keep
		}
	}
}

func (a *App) takeRetiring(itemID string) []<-chan struct{} {
	chans := a.retiring[itemID]
	if len(chans) == 0 {
		return nil
	}
	delete(a.retiring, itemID)
	var live []<-chan struct{}
	for _, ch := range chans {
		select {
		case <-ch:
		default:
			live = append(live, ch)
		}
	}
	return live
}

// Import 异步导入，完成后只加入壁纸库。applyTo 非空时再应用：显示器稳定 ID = 只应用到那块屏；
// ApplyAll = 所有显示器。导入本身不会改动任何显示器（修复 0.2.0“导入即应用到所有屏”）。
func (a *App) Import(path, applyTo string) {
	a.Do(func() {
		if a.d.Import == nil {
			a.lastImp = "此环境没有可用的导入器"
			return
		}
		if len(a.importQueue) >= MaxQueuedJobs {
			a.lastErr = "导入队列已满（256 项），请等待后重试"
			return
		}
		a.importQueue = append(a.importQueue, importRequest{path, applyTo})
		a.startNextImport()
	})
}

// CancelImports stops the active importer and drops queued requests. Completed
// items remain in the library; cancellation never auto-applies a partial batch.
func (a *App) CancelImports() {
	a.Do(func() {
		a.importQueue = nil
		a.importCancelled = true
		if a.importCancel != nil {
			a.importCancel()
		}
		a.lastImp = "导入已取消；已完成的项目保留在壁纸库"
	})
}

// Imports may replace complete item directories, so they are serialized with
// encoder commits and removals. Finite import requests take priority over queued
// optimizations; after the import queue drains, one encoder starts at a time.
func (a *App) advanceJobs() {
	a.startNextImport()
	if !a.importing {
		a.startNextTranscode()
	}
}

func (a *App) startNextImport() {
	if a.importing || a.cleaning || a.transcodingID != "" || len(a.removing) > 0 || len(a.importQueue) == 0 || a.ctx.Err() != nil {
		return
	}
	next := a.importQueue[0]
	a.importQueue = a.importQueue[1:]
	a.beginImport(next.path, next.applyTo)
}

func (a *App) beginImport(path, applyTo string) {
	if a.d.Import == nil {
		a.lastImp = "此环境没有可用的导入器"
		return
	}
	a.importing = true
	a.importCancelled = false
	a.lastImp = "正在导入 " + filepath.Base(path) + " …"
	ctx, cancel := context.WithCancel(a.ctx)
	a.importCancel = cancel
	// App 拥有导入任务；导入完成或 ctx 取消并收尾后退出。
	go func() {
		defer cancel()
		paths := []string{path}
		var err error
		if st, e := os.Stat(path); e == nil && st.IsDir() {
			paths, err = importer.CollectSources(path)
		}
		if err != nil {
			a.Do(func() {
				a.importing, a.importCancel = false, nil
				a.lastImp = "导入失败: " + err.Error()
				if a.importCancelled {
					a.lastImp = "导入已取消；已完成的项目保留在壁纸库"
				} else {
					a.lastErr = a.lastImp
				}
				a.advanceJobs()
			})
			return
		}
		imported, failed := 0, 0
		firstID, firstTitle := "", ""
		var failures []string
		var warnings []string
		for i, source := range paths {
			if ctx.Err() != nil {
				break
			}
			res, e := a.d.Import(ctx, source)
			if e != nil {
				failed++
				if len(failures) < 3 {
					failures = append(failures, filepath.Base(source)+": "+e.Error())
				}
				a.log.Warn("import failed", "file", telemetry.HashPath(source), "err", e)
				continue
			}
			if res == nil {
				failed++
				continue
			}
			imported++
			for _, warning := range res.Warnings {
				if len(warnings) < 3 {
					warnings = append(warnings, warning)
				}
			}
			if firstID == "" {
				firstID, firstTitle = res.Item.ID, res.Item.Title
			}
			progress := fmt.Sprintf("正在导入 %d/%d：%s", i+1, len(paths), res.Item.Title)
			result := *res
			a.Do(func() {
				old := a.d.Store.Library.Find(result.Item.ID)
				replacing := old != nil
				if replacing && result.Item.Props == (config.ItemProps{}) {
					result.Item.Props = old.Props // 重新导入同一壁纸时保留用户调好的组属性
				}
				a.invalidateItemWork(result.Item.ID)
				a.d.Store.Library.Upsert(result.Item)
				a.refreshItemRenditions(result.Item.ID)
				if replacing {
					a.refreshItemScreens(result.Item.ID)
				}
				a.saveLibrary()
				a.lastImp = progress
				if a.d.Store.Config.Render.AutoTranscode && result.Item.Transcode == "recommended" && a.d.Transcode != nil {
					a.transcode(result.Item.ID)
				}
			})
		}
		cancelled := ctx.Err() != nil
		a.Do(func() {
			a.importing, a.importCancel = false, nil
			defer a.advanceJobs()
			if cancelled || a.importCancelled {
				a.lastImp = "导入已取消；已完成的项目保留在壁纸库"
				a.refreshStorage()
				return
			}
			switch {
			case imported == 1 && failed == 0:
				a.lastImp = "已导入「" + firstTitle + "」"
			case imported > 0:
				a.lastImp = fmt.Sprintf("已导入 %d 项，失败 %d 项", imported, failed)
			default:
				a.lastImp = "导入失败"
			}
			if len(failures) > 0 {
				a.lastImp += "：" + joinNotes(failures)
			}
			if len(warnings) > 0 {
				a.lastImp += "（" + joinNotes(warnings) + "）"
			}
			if imported > 0 {
				switch {
				case applyTo == ApplyAll:
					if !a.spanning() {
						a.d.Store.Assign.Displays = map[string]string{}
					}
					a.assign("", firstID)
				case applyTo != "":
					if s := a.screens[desktop.DisplayID(applyTo)]; s != nil {
						a.assign(applyTo, firstID)
						a.lastImp += "，已将首项应用到「" + a.labelOf(s) + "」"
					}
				}
			}
			if failed > 0 {
				a.lastErr = a.lastImp
			}
			a.refreshStorage()
		})
	}()
}

func joinNotes(n []string) string {
	s := ""
	for _, x := range n {
		if x == "" {
			continue
		}
		if s != "" {
			s += "；"
		}
		s += x
	}
	return s
}

// Remove 从库中删除壁纸（同时删除包目录与缓存）。
func (a *App) Remove(itemID string) {
	a.Do(func() {
		if a.importing {
			a.lastErr = "导入进行中，请完成后再删除壁纸"
			return
		}
		if a.cleaning {
			a.lastErr = "清理缓存进行中，请完成后再删除壁纸"
			return
		}
		if a.d.Store.Library.Find(itemID) == nil {
			return
		}
		if _, pending := a.removing[itemID]; pending {
			return
		}
		a.invalidateItemWork(itemID)
		generation := a.itemGeneration[itemID]
		a.removing[itemID] = generation
		var wait sync.WaitGroup
		if a.transcodingID == itemID && a.transcodeDone != nil {
			done := a.transcodeDone
			wait.Add(1)
			go func() { <-done; wait.Done() }()
		}
		for _, s := range a.screens {
			if s.item != nil && s.item.ID == itemID {
				a.closeScreen(s, false)
				wait.Add(1)
				done := s.ops.barrier()
				go func() {
					defer wait.Done()
					select {
					case <-done:
					case <-a.closing:
					}
				}()
			}
		}
		// Also wait for screens that recently switched away from itemID or were
		// unplugged while their async renderer teardown was still in flight.
		for _, done := range a.takeRetiring(itemID) {
			wait.Add(1)
			ch := done
			go func() {
				defer wait.Done()
				select {
				case <-ch:
				case <-a.closing:
				}
			}()
		}
		// App 拥有删除等待器；编码与屏幕操作释放后投递删除并退出。
		go func() { wait.Wait(); a.Do(func() { a.removeItemNow(itemID, generation) }) }()
	})
}

func (a *App) removeItemNow(itemID string, generation uint64) {
	defer a.advanceJobs()
	if a.removing[itemID] != generation || a.itemGeneration[itemID] != generation {
		return
	}
	delete(a.removing, itemID)
	it := a.d.Store.Library.Find(itemID)
	if it == nil {
		return
	}
	var tombstones []string
	if managedChild(a.d.Store.Paths.Library, it.Dir) {
		moved, err := tombstone(it.Dir)
		if err != nil {
			a.lastErr = "暂无法删除壁纸文件，请稍后重试：" + err.Error()
			a.refreshItemScreens(itemID)
			return
		}
		if moved != "" {
			tombstones = append(tombstones, moved)
		}
	}
	if it.Snapshot != "" {
		dir := filepath.Dir(it.Snapshot)
		if managedChild(a.d.Store.Paths.Cache, dir) {
			moved, err := tombstone(dir)
			if err != nil {
				a.lastErr = "壁纸已移除，旧预览暂无法清理：" + err.Error()
			} else if moved != "" {
				tombstones = append(tombstones, moved)
			}
		}
	}
	cacheDir := filepath.Join(a.d.Store.Paths.Cache, itemID)
	if managedChild(a.d.Store.Paths.Cache, cacheDir) {
		if moved, err := tombstone(cacheDir); err == nil && moved != "" {
			tombstones = append(tombstones, moved)
		} else if err != nil {
			a.lastErr = err.Error()
		}
	}
	a.d.Store.Library.Remove(itemID)
	a.evictItemRenditions(itemID)
	a.saveLibrary()
	if a.d.Store.Assign.SpanItem == itemID {
		a.d.Store.Assign.SpanItem = ""
	}
	if a.d.Store.Assign.Default == itemID {
		a.d.Store.Assign.Default = ""
	}
	for k, v := range a.d.Store.Assign.Displays {
		if v == itemID {
			delete(a.d.Store.Assign.Displays, k)
		}
	}
	a.saveAssign()
	for _, s := range a.screens {
		if s.item != nil && s.item.ID == itemID {
			s.item = nil
			if s.surf != nil {
				surf := s.surf
				s.ops.enqueue(func() { _ = surf.SetVisible(false) })
			}
		}
	}
	for key := range a.d.Store.State.Positions {
		if strings.HasSuffix(key, "|"+itemID) {
			delete(a.d.Store.State.Positions, key)
		}
	}
	a.saveState()
	// App 拥有墓碑清理；有限路径删除及结果投递完成后退出。
	go func() {
		var err error
		for _, path := range tombstones {
			if e := os.RemoveAll(path); e != nil {
				err = e
			}
		}
		a.Do(func() {
			if err != nil {
				a.lastErr = "文件清理失败：" + err.Error()
			}
			a.refreshStorage()
		})
	}()
}

// A package update can retain its ID while changing entry, snapshot and metadata.
// Refresh active copies explicitly instead of treating same-ID assignment as a no-op.
func (a *App) refreshItemScreens(itemID string) {
	for _, s := range a.screens {
		if s.item == nil || s.item.ID != itemID {
			continue
		}
		m := a.ctl.Display(s.n)
		if m != nil && m.Level() >= visibility.L4 {
			a.assignItem(s)
			if m.State() == visibility.StateFailed {
				m.Retry(a.now())
				continue
			}
			a.setScreenL4Stopped(s)
			continue
		}
		if s.r != nil {
			a.closeScreen(s, false)
		}
		a.assignItem(s)
		if m != nil && m.State() == visibility.StateFailed {
			m.Retry(a.now())
			continue
		}
		a.startScreen(s, 0)
	}
}

// Invalidate queued/running work before replacing or removing a library identity.
func (a *App) invalidateItemWork(itemID string) {
	for _, j := range a.renditionJobs {
		if j.item == itemID {
			j.state = "已取消"
		}
	}
	delete(a.removing, itemID)
	a.itemGeneration[itemID]++
	if a.transcodingID == itemID && a.transcodeCancel != nil {
		a.transcodeCancel()
	}
	pending := a.transcodeQueue[:0]
	for _, id := range a.transcodeQueue {
		if id != itemID {
			pending = append(pending, id)
		}
	}
	a.transcodeQueue = pending
	delete(a.transc, itemID)
}

// Transcode 对一个“建议转码”的条目运行编码器（F-07），完成后替换入口并重启正在播放它的屏幕。
// 原文件保留，转码失败不影响原条目。
func (a *App) Transcode(itemID string) { a.Do(func() { a.transcode(itemID) }) }

func (a *App) transcode(itemID string) {
	it := a.d.Store.Library.Find(itemID)
	if it == nil || a.transc[itemID] == "正在转码…" || a.transc[itemID] == "等待转码…" {
		return
	}
	if a.d.Transcode == nil {
		a.transc[itemID] = "本机没有可用的编码器"
		return
	}
	if len(a.transcodeQueue) >= MaxQueuedJobs {
		a.transc[itemID] = "转码队列已满，请稍后重试"
		return
	}
	a.transc[itemID] = "等待转码…"
	a.transcodeQueue = append(a.transcodeQueue, itemID)
	a.startNextTranscode()
}

func (a *App) startNextTranscode() {
	if a.transcodingID != "" || a.importing || a.cleaning || len(a.removing) > 0 || a.ctx.Err() != nil {
		return
	}
	for len(a.transcodeQueue) > 0 {
		id := a.transcodeQueue[0]
		a.transcodeQueue = a.transcodeQueue[1:]
		if a.d.Store.Library.Find(id) == nil {
			delete(a.transc, id)
			continue
		}
		if a.d.Transcode == nil {
			a.transc[id] = "本机没有可用的编码器"
			continue
		}
		a.beginTranscode(id)
		if a.transcodingID != "" {
			return
		}
	}
	a.startNextRendition()
}

func (a *App) beginTranscode(itemID string) {
	it := a.d.Store.Library.Find(itemID)
	if it == nil {
		return
	}
	dirInfo, err := os.Stat(it.Dir)
	if err != nil {
		a.transc[itemID] = "转码失败：" + err.Error()
		return
	}
	generation := a.itemGeneration[itemID]
	a.transcodingID = itemID
	ctx, cancel := context.WithCancel(a.ctx)
	a.transcodeCancel = cancel
	done := make(chan struct{})
	a.transcodeDone = done
	ext := a.d.TranscodeExt
	if ext == "" {
		ext = ".mp4"
	}
	src := filepath.Join(it.Dir, filepath.FromSlash(it.Entry))
	sourceInfo, _ := os.Stat(src)
	rel := "optimized" + ext
	dst := filepath.Join(it.Dir, rel)
	a.transc[itemID] = "正在转码…"
	enc := a.d.Transcode
	probe := a.d.ProbeMedia
	strict := a.encoderInfo.Strict
	// App 拥有转码任务；编码完成或 ctx 取消后清理临时文件并退出。
	go func() {
		f, err := os.CreateTemp(a.d.Store.Paths.Library, ".transcode-*"+ext)
		tmp := ""
		if err == nil {
			tmp = f.Name()
			err = f.Close()
		}
		if err == nil {
			err = enc(ctx, src, tmp)
		}
		var info *media.Info
		if err == nil && probe != nil {
			info, err = probe(ctx, tmp)
			if err == nil && (info == nil || info.Kind != "video" || (info.Codec != "h264" && info.Codec != "hevc")) {
				err = errors.New("encoder output is not a supported video")
			}
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		// The encoder and probe have returned and released their file handles.
		// Removal must wait for this before renaming the source directory on Windows.
		close(done)
		committed := make(chan struct{})
		a.Do(func() {
			defer close(committed)
			defer func() {
				if tmp != "" {
					_ = os.Remove(tmp)
				}
			}()

			cancel()
			a.transcodeCancel = nil
			a.transcodingID = ""
			defer a.advanceJobs()
			p := a.d.Store.Library.Find(itemID)
			if p == nil || a.itemGeneration[itemID] != generation {
				return
			}
			currentInfo, identityErr := os.Stat(p.Dir)
			if identityErr != nil || !os.SameFile(dirInfo, currentInfo) {
				delete(a.transc, itemID)
				return
			}
			if err == nil && sourceInfo != nil {
				current, checkErr := os.Stat(src)
				if checkErr != nil || !os.SameFile(sourceInfo, current) || current.Size() != sourceInfo.Size() || !current.ModTime().Equal(sourceInfo.ModTime()) {
					err = errors.New("source media changed during encoding; retry the new source")
				}
			}
			if err == nil {
				err = os.Rename(tmp, dst)
			}
			if err != nil {
				a.transc[itemID] = "转码失败：" + err.Error()
				a.log.Warn("transcode failed", "item", itemID, "err", err)
				return
			}
			delete(a.transc, itemID)
			p.Entry, p.Transcode = rel, "done"
			if strict {
				p.Note = "已转码为 H.264、30 fps、无音轨"
			} else {
				p.Note = "转码完成；帧率和音轨依实际编码器能力"
			}
			if info != nil {
				p.Codec, p.Width, p.Height, p.DurationSec = info.Codec, info.W, info.H, info.DurationSec
			}
			if st, err := os.Stat(dst); err == nil {
				p.SizeBytes = st.Size()
			}
			a.saveLibrary()
			a.refreshItemRenditions(itemID)
			a.log.Info("transcoded", "item", itemID)
			a.refreshStorage()
			for _, s := range a.sortedScreens() {
				if s.item != nil && s.item.ID == itemID {
					a.assignItem(s)
					if m := a.ctl.Display(s.n); m != nil && m.Level() >= visibility.L4 {
						a.setScreenL4Stopped(s)
					} else {
						a.closeScreen(s, false)
						a.startScreen(s, 0)
					}
				}
			}
		})
		select {
		case <-committed:
		case <-a.stopped:
		}
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
}
