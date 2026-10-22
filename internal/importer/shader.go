package importer

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"wpapp/internal/render/shader"
	"wpapp/internal/wpk"
)

func validateShaderArchive(path string) error {
	z, e := zip.OpenReader(path)
	if e != nil {
		return e
	}
	defer z.Close()
	var m *wpk.Manifest
	read := func(f *zip.File, max int64) ([]byte, error) {
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		b, e := io.ReadAll(io.LimitReader(r, max+1))
		if int64(len(b)) > max {
			return nil, fmt.Errorf("shader line 1: source too large")
		}
		return b, e
	}
	for _, f := range z.File {
		if f.Name == "manifest.json" {
			b, e := read(f, wpk.MaxManifest)
			if e != nil {
				return e
			}
			m, e = wpk.Parse(b)
			if e != nil {
				return e
			}
			break
		}
	}
	if m == nil || m.Type != "shader" {
		return nil
	}
	for _, f := range z.File {
		if f.Name == m.Entry {
			b, e := read(f, 1<<20)
			if e != nil {
				return e
			}
			for _, target := range []shader.Target{shader.HLSL, shader.MSL} {
				if _, e = shader.TranslateProperties(string(b), target, m.Properties); e != nil {
					return e
				}
			}
			return nil
		}
	}
	return fmt.Errorf("shader line 1: entry not found")
}
func importShader(src string, o Options) (*Result, error) {
	f, e := os.Open(src)
	if e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	if e != nil {
		return nil, e
	}
	for _, target := range []shader.Target{shader.HLSL, shader.MSL} {
		if _, e := shader.Translate(string(b), target); e != nil {
			return nil, e
		}
	}
	tmp, e := os.MkdirTemp("", "wallpaper-shader-*")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(tmp)
	if e = os.MkdirAll(filepath.Join(tmp, "content"), 0700); e != nil {
		return nil, e
	}
	entry := "content/main.frag"
	if e = os.WriteFile(filepath.Join(tmp, entry), b, 0600); e != nil {
		return nil, e
	}
	schema := 0
	m := &wpk.Manifest{Format: "wpk", Schema: &schema, ID: NewID(), Version: 1, Type: "shader", Entry: entry, Title: wpk.I18n{"en": strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))}, Shader: &wpk.Shader{Dialect: "shadertoy-glsl"}, Origin: &wpk.Origin{Kind: "native"}}
	if e = wpk.WriteManifest(tmp, m); e != nil {
		return nil, e
	}
	if e = shaderPlaceholder(filepath.Join(tmp, "preview.png"), b); e != nil {
		return nil, e
	}
	return importFolder(tmp, o)
}
func shaderPlaceholder(path string, source []byte) error {
	seed := sha256.Sum256(source)
	im := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			v := uint8((x + y) * 32 / 500)
			im.SetRGBA(x, y, color.RGBA{32 + seed[0]/4 + v, 32 + seed[1]/4 + v, 32 + seed[2]/4 + v, 255})
		}
	}
	f, e := os.Create(path)
	if e != nil {
		return e
	}
	e = png.Encode(f, im)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
