//go:build windows && shadergpu

package shader

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestHLSLGoldenCompilation(t *testing.T) {
	d, e := syscall.LoadDLL("d3dcompiler_47.dll")
	if e != nil {
		t.Skip(e)
	}
	d.Release()
	files, _ := filepath.Glob("testdata/*.glsl")
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			src, e := Translate(string(b), HLSL)
			if e != nil {
				t.Fatal(e)
			}
			blob, e := compileHLSL(src, "wp_fragment", "ps_5_0")
			if e != nil {
				t.Fatal(e)
			}
			release(&blob)
		})
	}
}
