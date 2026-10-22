package importer

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"sort"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// PreviewColors indexes only a local still preview, never video frames on the UI
// thread. File size and decoded dimensions are bounded before image allocation.
// Missing/invalid/oversized previews simply have no color index; import survives.
func PreviewColors(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const maxBytes = 16 << 20
	const maxPixels = 16 << 20
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxBytes {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPixels/cfg.Height {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return imageColors(img)
}

var colorNames = [][2]string{
	{"black", "黑色"}, {"white", "白色"}, {"gray", "灰色"},
	{"red", "红色"}, {"orange", "橙色"}, {"yellow", "黄色"},
	{"green", "绿色"}, {"cyan", "青色"}, {"blue", "蓝色"},
	{"purple", "紫色"}, {"pink", "粉色"}, {"brown", "棕色"},
}

func colorBucket(c color.NRGBA) int {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	delta := hi - lo
	if hi < .16 {
		return 0
	}
	if delta/hi < .18 {
		if hi > .91 {
			return 1
		}
		return 2
	}
	var hue float64
	switch hi {
	case r:
		hue = math.Mod((g-b)/delta, 6)
	case g:
		hue = (b-r)/delta + 2
	default:
		hue = (r-g)/delta + 4
	}
	hue *= 60
	if hue < 0 {
		hue += 360
	}
	if hue >= 15 && hue < 45 && hi < .65 {
		return 11
	}
	switch {
	case hue < 15 || hue >= 345:
		return 3
	case hue < 45:
		return 4
	case hue < 75:
		return 5
	case hue < 165:
		return 6
	case hue < 195:
		return 7
	case hue < 255:
		return 8
	case hue < 290:
		return 9
	default:
		return 10
	}
}

func imageColors(img image.Image) []string {
	bounds := img.Bounds()
	if bounds.Empty() {
		return nil
	}
	counts := make([]int, len(colorNames))
	total := 0
	nx, ny := min(32, bounds.Dx()), min(32, bounds.Dy())
	for y := 0; y < ny; y++ {
		for x := 0; x < nx; x++ {
			c := color.NRGBAModel.Convert(img.At(bounds.Min.X+x*bounds.Dx()/nx, bounds.Min.Y+y*bounds.Dy()/ny)).(color.NRGBA)
			if c.A < 128 {
				continue
			}
			counts[colorBucket(c)]++
			total++
		}
	}
	indices := make([]int, len(counts))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool { return counts[indices[i]] > counts[indices[j]] })
	var result []string
	for _, index := range indices {
		if counts[index] == 0 || counts[index]*10 < total || len(result) >= 6 {
			break
		}
		result = append(result, colorNames[index][:]...)
	}
	return result
}
