//go:build darwin && shadergpu

package shader

import (
	"github.com/ebitengine/purego/objc"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMetalGoldenCompilation(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	device, e := metalDevice()
	if e != nil {
		t.Skip(e)
	}
	defer device.Send(sel("release"))
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	defer pool.Send(sel("drain"))
	files, e := filepath.Glob("testdata/*.glsl")
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, e := os.ReadFile(f)
			if e != nil {
				t.Fatal(e)
			}
			src, e := Translate(string(b), MSL)
			if e != nil {
				t.Fatal(e)
			}
			lib, e := compileMetal(device, src)
			if e != nil {
				t.Fatal(e)
			}
			lib.Send(sel("release"))
		})
	}
}
