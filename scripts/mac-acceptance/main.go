//go:build darwin

// Opt-in macOS real-device acceptance; displays video on the primary desktop.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"wpapp/internal/desktop"
	"wpapp/internal/desktop/mac"
	"wpapp/internal/platform/macos"
	"wpapp/internal/render"
	"wpapp/internal/render/avf"
	"wpapp/internal/visibility"
)

func init() { runtime.LockOSThread() }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "l3bench:", err)
		os.Exit(1)
	}
}

func run() error {
	video := flag.String("video", "", "absolute or relative video path")
	cycles := flag.Int("cycles", 5, "number of release/resume cycles")
	observe := flag.Duration("observe", 0, "only display video for this duration (visual inspection)")
	flag.Parse()
	if *video == "" {
		return errors.New("-video is required")
	}
	path, err := filepath.Abs(*video)
	if err != nil {
		return err
	}
	if st, err := os.Stat(path); err != nil {
		return err
	} else if !st.Mode().IsRegular() {
		return errors.New("video must be a regular file")
	}
	queue := make(chan func(), 64)
	macos.Main = func(fn func()) {
		done := make(chan struct{})
		queue <- func() { fn(); close(done) }
		<-done
	}
	if err := macos.Load(); err != nil {
		return err
	}
	pool := macos.Class("NSAutoreleasePool").Send(macos.Sel("new"))
	app := macos.Class("NSApplication").Send(macos.Sel("sharedApplication"))
	if !objc.Send[bool](app, macos.Sel("setActivationPolicy:"), int64(1)) {
		pool.Send(macos.Sel("drain"))
		return errors.New("setActivationPolicy failed")
	}
	pool.Send(macos.Sel("drain"))
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	var pump func(uintptr, float64, bool) int32
	purego.RegisterLibFunc(&pump, cf, "CFRunLoopRunInMode")
	address, err := purego.Dlsym(cf, "kCFRunLoopDefaultMode")
	if err != nil {
		return err
	}
	mode := **(**uintptr)(unsafe.Pointer(&address))
	finished := make(chan error, 1)
	go func() { finished <- scenario(path, *observe, *cycles) }()
	for {
		pool := macos.Class("NSAutoreleasePool").Send(macos.Sel("new"))
	drain:
		for {
			select {
			case fn := <-queue:
				fn()
			default:
				break drain
			}
		}
		pump(mode, 0.01, true)
		pool.Send(macos.Sel("drain"))
		select {
		case err := <-finished:
			return err
		default:
		}
	}
}

func scenario(video string, observe time.Duration, cycles int) (err error) {
	host := mac.New(mac.Options{})
	if err = host.Start(); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, host.Close()) }()
	displays := host.Displays()
	if len(displays) == 0 {
		return errors.New("no displays")
	}
	surface, err := host.CreateSurface(displays[0].ID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, surface.Close()) }()
	r := avf.New()
	defer func() { err = errors.Join(err, r.Close()) }()
	if err = r.Load(render.Item{Path: video, Kind: "video"}, surface, false); err != nil {
		return err
	}
	if observe > 0 {
		return hold(r, "visual_L0", visibility.L0, observe)
	}
	if err = checkExactSeek(r, surface, video); err != nil {
		return err
	}
	if err = hold(r, "initial_L0", visibility.L0, 15*time.Second); err != nil {
		return err
	}
	if err = hold(r, "initial_L1", visibility.L1, 10*time.Second); err != nil {
		return err
	}
	before := r.Stats().Pos
	if err = hold(r, "initial_L3", visibility.L3, 10*time.Second); err != nil {
		return err
	}
	if err = checkStill(surface, true); err != nil {
		return err
	}
	if err = resume(r, "initial_resume", before); err != nil {
		return err
	}
	if err = waitForVideo(r, surface); err != nil {
		return err
	}
	for i := 1; i <= cycles; i++ {
		if err = hold(r, fmt.Sprintf("cycle_%d_L0", i), visibility.L0, 10*time.Second); err != nil {
			return err
		}
		before = r.Stats().Pos
		if err = hold(r, fmt.Sprintf("cycle_%d_L3", i), visibility.L3, 10*time.Second); err != nil {
			return err
		}
		if err = checkStill(surface, true); err != nil {
			return err
		}
		if err = resume(r, fmt.Sprintf("cycle_%d_resume", i), before); err != nil {
			return err
		}
		if err = waitForVideo(r, surface); err != nil {
			return err
		}
	}
	if err = r.SetLevel(visibility.L3); err != nil {
		return err
	}
	options := render.DefaultPlaybackOptions()
	options.Volume = 37
	if err = r.UpdatePlayback(options); err != nil {
		return fmt.Errorf("UpdatePlayback in L3: %w", err)
	}
	if err = r.SetLevel(visibility.L1); err != nil {
		return err
	}
	// Allow asynchronous readiness/seek to settle before checking paused motion.
	if err = wait(r, 3*time.Second); err != nil {
		return err
	}
	start := r.Stats().Pos
	loops := r.Stats().Loops
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err = wait(r, 50*time.Millisecond); err != nil {
			return err
		}
		st := r.Stats()
		if math.Abs(st.Pos-start) > 0.05 || st.Loops != loops {
			return fmt.Errorf("L3 -> UpdatePlayback -> L1 advanced: %.6f -> %.6f", start, st.Pos)
		}
	}
	if err = emit(map[string]any{"stage": "edge_paused", "update_playback_ok": true, "paused": true, "start_pos": start, "end_pos": r.Stats().Pos, "observation_s": 3}); err != nil {
		return err
	}
	return resume(r, "edge_resume", r.Stats().Pos)
}

// Settled paused samples distinguish accurate seeking from the cached Start value.
func checkExactSeek(r *avf.Renderer, surface desktop.Surface, video string) error {
	for _, start := range []float64{13.2, 38.15, 599} {
		if err := r.Load(render.Item{Path: video, Kind: "video", Start: start}, surface, true); err != nil {
			return err
		}
		if err := wait(r, 4*time.Second); err != nil {
			return err
		}
		actual := r.Stats().Pos
		if err := emit(map[string]any{"stage": "exact_paused_seek", "requested": start, "actual": actual}); err != nil {
			return err
		}
		if math.Abs(actual-start) > .05 {
			return fmt.Errorf("inaccurate paused seek: requested %.3f, actual %.3f", start, actual)
		}
	}
	if err := r.SetLevel(visibility.L0); err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second)
	for r.Stats().Loops == 0 {
		if time.Now().After(deadline) {
			return errors.New("loop boundary did not advance")
		}
		if err := wait(r, 50*time.Millisecond); err != nil {
			return err
		}
	}
	if err := emit(map[string]any{"stage": "loop_boundary", "loops": r.Stats().Loops, "pos": r.Stats().Pos}); err != nil {
		return err
	}
	return r.Load(render.Item{Path: video, Kind: "video"}, surface, false)
}

// These assertions inspect the actual native layer tree, not cached playback time.
func checkStill(surface desktop.Surface, want bool) error {
	var held bool
	var layers uint
	macos.Main(func() {
		root := objc.ID(surface.Native()).Send(macos.Sel("layer"))
		held = root.Send(macos.Sel("contents")) != 0
		layers = objc.Send[uint](root.Send(macos.Sel("sublayers")), macos.Sel("count"))
	})
	if held != want || (want && layers != 0) {
		return fmt.Errorf("pause frame held=%t video_layers=%d, want held=%t", held, layers, want)
	}
	return emit(map[string]any{"stage": "native_pause_frame", "held": held, "video_layers": layers})
}
func waitForVideo(r *avf.Renderer, surface desktop.Surface) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var ready bool
		macos.Main(func() {
			root := objc.ID(surface.Native()).Send(macos.Sel("layer"))
			layer := root.Send(macos.Sel("sublayers")).Send(macos.Sel("lastObject"))
			ready = layer != 0 && objc.Send[bool](layer, macos.Sel("isReadyForDisplay")) && !objc.Send[bool](layer, macos.Sel("isHidden")) && root.Send(macos.Sel("contents")) == 0
		})
		if ready {
			return emit(map[string]any{"stage": "native_video_ready", "ready": true, "still_released": true})
		}
		if err := wait(r, 25*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("video layer did not become visible and ready; still not released")
}

func wait(r *avf.Renderer, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case err := <-r.Done():
		if err == nil {
			return errors.New("renderer stopped unexpectedly")
		}
		return err
	case <-timer.C:
		if msg := r.Stats().LastErr; msg != "" {
			return errors.New(msg)
		}
		return nil
	}
}

func hold(r *avf.Renderer, stage string, level visibility.Level, duration time.Duration) error {
	if err := r.SetLevel(level); err != nil {
		return err
	}
	if err := wait(r, duration); err != nil {
		return err
	}
	return measure(r, stage)
}

func resume(r *avf.Renderer, stage string, before float64) error {
	started := time.Now()
	if err := r.SetLevel(visibility.L0); err != nil {
		return err
	}
	elapsed := time.Since(started)
	first := r.Stats().Pos
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := r.Stats()
		if st.LastErr != "" {
			return errors.New(st.LastErr)
		}
		if st.Pos > before+0.2 {
			return emit(map[string]any{"stage": stage, "pre_l3_pos": before, "resume_pos": first, "advance_pos": st.Pos, "set_level_ms": float64(elapsed) / float64(time.Millisecond), "first_advance_ms": float64(time.Since(started)) / float64(time.Millisecond)})
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: position did not exceed %.6f within 15s (last %.6f)", stage, before+0.2, st.Pos)
		}
		if err := wait(r, 50*time.Millisecond); err != nil {
			return err
		}
	}
}

var footprintValue = regexp.MustCompile(`(?im)^\s*(?:phys_footprint|physical footprint|footprint)\s*[:=]\s*([0-9,]+)`)
var integer = regexp.MustCompile(`[0-9][0-9,]*`)

func measure(r *avf.Renderer, stage string) error {
	pid := strconv.Itoa(os.Getpid())
	// This macOS footprint includes categories by default; --categories is unsupported.
	output, err := exec.Command("/usr/bin/footprint", "-p", pid, "-f", "bytes").CombinedOutput()
	if err != nil {
		return fmt.Errorf("footprint: %w: %s", err, output)
	}
	match := footprintValue.FindStringSubmatch(string(output))
	// Auxiliary tables may put the ledger name after the numeric columns.
	for _, line := range strings.Split(string(output), "\n") {
		for _, field := range strings.Fields(line) {
			if strings.TrimSuffix(field, ":") == "phys_footprint" {
				if value := integer.FindString(line); value != "" {
					match = []string{"", value}
				}
			}
		}
	}
	if len(match) != 2 {
		return fmt.Errorf("footprint: physical footprint not found: %s", output)
	}
	phys, err := strconv.ParseUint(strings.ReplaceAll(match[1], ",", ""), 10, 64)
	if err != nil {
		return err
	}
	row := map[string]any{"stage": stage, "pos": r.Stats().Pos, "phys_footprint": phys, "footprint_command": "footprint -p PID -f bytes", "memory_unit": "bytes"}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "IOSurface") {
			continue
		}
		var numbers []uint64
		for _, token := range integer.FindAllString(strings.ReplaceAll(line, "IOSurface", ""), -1) {
			n, err := strconv.ParseUint(strings.ReplaceAll(token, ",", ""), 10, 64)
			if err != nil {
				return err
			}
			numbers = append(numbers, n)
		}
		row["iosurface_columns"] = numbers // Numeric columns in footprint's printed order.
	}
	output, err = exec.Command("/bin/ps", "-M", "-p", pid).Output()
	if err != nil {
		return fmt.Errorf("ps: %w", err)
	}
	lines := strings.Count(string(output), "\n")
	row["ps_lines"] = lines // Equivalent to ps -M -p PID | wc -l, including header.
	row["thread_count"] = lines - 1
	return emit(row)
}

func emit(row map[string]any) error { return json.NewEncoder(os.Stdout).Encode(row) }
