package web

import (
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"wpapp/internal/ipc"
	"wpapp/internal/render"
)

type lifecycleSurface struct{}

func (lifecycleSurface) Native() uintptr         { return 1 }
func (lifecycleSurface) Bounds() image.Rectangle { return image.Rect(0, 0, 100, 100) }
func (lifecycleSurface) SetVisible(bool) error   { return nil }
func (lifecycleSurface) Close() error            { return nil }
func TestHostProcessHelper(t *testing.T) {
	if os.Getenv("WPAPP_WEB_LIFECYCLE_HELPER") != "1" {
		return
	}
	c := ipc.New(pipeIO{os.Stdin, os.Stdout}, func(string, json.RawMessage) (any, *ipc.Error) { return struct{}{}, nil }, nil)
	<-c.Done()
	os.Exit(0)
}
func TestWebHostOnlyStartsOnWebLoad(t *testing.T) {
	r := New("not-an-executable")
	started := 0
	r.command = func() *exec.Cmd {
		started++
		cmd := exec.Command(os.Args[0], "-test.run=^TestHostProcessHelper$")
		cmd.Env = append(os.Environ(), "WPAPP_WEB_LIFECYCLE_HELPER=1")
		return cmd
	}
	if e := r.Configure(render.DefaultPlaybackOptions()); e != nil {
		t.Fatal(e)
	}
	if started != 0 || r.cmd != nil {
		t.Fatal("host started at construction/configuration")
	}
	for _, kind := range []string{"shader", "video", "image", ""} {
		if e := r.Load(render.Item{Kind: kind}, lifecycleSurface{}, false); e == nil {
			t.Fatal("accepted", kind)
		}
	}
	if started != 0 || r.cmd != nil {
		t.Fatal("non-web item spawned host")
	}
	if runtime.GOOS == "linux" {
		if e := r.Load(render.Item{Kind: "web"}, lifecycleSurface{}, false); e == nil {
			t.Fatal("Linux support falsely advertised")
		}
		return
	}
	if e := r.Load(render.Item{Kind: "web"}, lifecycleSurface{}, false); e != nil {
		t.Fatal(e)
	}
	if started != 1 || r.cmd == nil {
		t.Fatal("web load did not spawn exactly one host")
	}
	exited := r.exited
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned before host process exited")
	}
	if r.cmd != nil {
		t.Fatal("host retained after unload")
	}
	select {
	case e := <-r.Done():
		t.Fatal("intentional close reported as crash", e)
	default:
	}
}
