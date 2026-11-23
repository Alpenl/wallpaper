package importer

import (
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

const maxShaderBytes = 1 << 20

func readShaderFileLimited(src string) ([]byte, error) {
	if err := rejectUnsafeHostPath(src); err != nil {
		return nil, err
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("shader line 1: source must be a regular file")
	}
	if st.Size() > maxShaderBytes {
		return nil, fmt.Errorf("shader line 1: source exceeds 1 MiB limit")
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxShaderBytes+1))
	f.Close()
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxShaderBytes {
		return nil, fmt.Errorf("shader line 1: source exceeds 1 MiB limit")
	}
	return b, nil
}

func validateStagedShader(stagedDir string, m *wpk.Manifest, o Options) error {
	entryPath := filepath.Join(stagedDir, filepath.FromSlash(m.Entry))
	st, err := os.Stat(entryPath)
	if err != nil {
		return fmt.Errorf("shader line 1: entry not found: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("shader line 1: entry must be a regular file")
	}
	if st.Size() > maxShaderBytes {
		return fmt.Errorf("shader line 1: source exceeds 1 MiB limit")
	}
	if o.ValidateShader != nil {
		_, err := o.ValidateShader(entryPath)
		return err
	}
	b, err := readShaderFileLimited(entryPath)
	if err != nil {
		return err
	}
	for _, target := range []shader.Target{shader.HLSL, shader.MSL} {
		if _, err := shader.TranslateProperties(string(b), target, m.Properties); err != nil {
			return err
		}
	}
	return nil
}

func importShader(src string, o Options) (*Result, error) {
	b, err := readShaderFileLimited(src)
	if err != nil {
		return nil, err
	}
	var props []wpk.PropertyDef
	if o.ValidateShader != nil {
		props, err = o.ValidateShader(src)
		if err != nil {
			return nil, err
		}
	} else {
		for _, target := range []shader.Target{shader.HLSL, shader.MSL} {
			if _, err := shader.TranslateProperties(string(b), target, nil); err != nil {
				return nil, err
			}
		}
	}
	tmp, err := os.MkdirTemp("", "wallpaper-shader-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err = os.MkdirAll(filepath.Join(tmp, "content"), 0700); err != nil {
		return nil, err
	}
	entry := "content/main.frag"
	if err = os.WriteFile(filepath.Join(tmp, entry), b, 0600); err != nil {
		return nil, err
	}
	schema := 0
	m := &wpk.Manifest{
		Format:     "wpk",
		Schema:     &schema,
		ID:         NewID(),
		Version:    1,
		Type:       "shader",
		Entry:      entry,
		Title:      wpk.I18n{"en": strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))},
		Shader:     &wpk.Shader{Dialect: "shadertoy-glsl"},
		Properties: props,
		Origin:     &wpk.Origin{Kind: "native"},
	}
	if err = wpk.WriteManifest(tmp, m); err != nil {
		return nil, err
	}
	preview := filepath.Join(tmp, "preview.png")
	if o.ShaderSnapshot == nil || o.ShaderSnapshot(tmp, preview) != nil {
		if err = shaderPlaceholder(preview, b); err != nil {
			return nil, err
		}
	}
	return importFolderValidated(tmp, o, true)
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
