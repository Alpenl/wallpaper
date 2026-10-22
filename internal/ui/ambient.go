package ui

import (
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"time"

	"github.com/egoist/mygo/ui"

	"wpapp/internal/config"
)

// 属性栏顶部色彩氛围的位图：缩到 ambientW×ambientH 再做几遍盒式模糊，界面把它放大铺开。
// 放大时的插值让它看起来像大半径高斯模糊，但只在缩略图变化时算一次，不用逐帧的 Blur 效果。
const ambientW, ambientH = 48, 27

type ambientBitmap struct {
	bm  *ui.Bitmap
	mod time.Time
}

// ambientOf 返回条目首帧截图的模糊小图；没有截图时返回 nil（调用方画渐变占位）。
func (s *Settings) ambientOf(it config.Item) *ui.Bitmap {
	if it.Snapshot == "" {
		return nil
	}
	st, err := os.Stat(it.Snapshot)
	if err != nil {
		return nil
	}
	if a, ok := s.ambients[it.ID]; ok && a.mod.Equal(st.ModTime()) {
		return a.bm
	}
	f, err := os.Open(it.Snapshot)
	if err != nil {
		return nil
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return nil
	}
	bm := ui.NewBitmap(blurSmall(src))
	if s.ambients == nil {
		s.ambients = map[string]ambientBitmap{}
	}
	s.ambients[it.ID] = ambientBitmap{bm: bm, mod: st.ModTime()}
	return bm
}

// blurSmall 按面积平均缩成 ambientW×ambientH（保持 cover 裁切），再水平、垂直各做三遍半径 2 的盒式模糊。
func blurSmall(src image.Image) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	// cover：按较小比例取中间区域
	cw, ch := sw, sw*ambientH/ambientW
	if ch > sh {
		cw, ch = sh*ambientW/ambientH, sh
	}
	ox, oy := b.Min.X+(sw-cw)/2, b.Min.Y+(sh-ch)/2
	px := make([][3]float64, ambientW*ambientH)
	for y := 0; y < ambientH; y++ {
		y0, y1 := oy+y*ch/ambientH, oy+(y+1)*ch/ambientH
		for x := 0; x < ambientW; x++ {
			x0, x1 := ox+x*cw/ambientW, ox+(x+1)*cw/ambientW
			var r, g, bl, n float64
			// 每格最多取 8×8 个采样点，避免大图逐像素累加
			stepX, stepY := max(1, (x1-x0)/8), max(1, (y1-y0)/8)
			for yy := y0; yy < max(y1, y0+1); yy += stepY {
				for xx := x0; xx < max(x1, x0+1); xx += stepX {
					cr, cg, cb, _ := src.At(xx, yy).RGBA()
					r, g, bl, n = r+float64(cr>>8), g+float64(cg>>8), bl+float64(cb>>8), n+1
				}
			}
			px[y*ambientW+x] = [3]float64{r / n, g / n, bl / n}
		}
	}
	box := func(horizontal bool) {
		out := make([][3]float64, len(px))
		for y := 0; y < ambientH; y++ {
			for x := 0; x < ambientW; x++ {
				var sum [3]float64
				n := 0.0
				for d := -2; d <= 2; d++ {
					xx, yy := x, y
					if horizontal {
						xx = min(ambientW-1, max(0, x+d))
					} else {
						yy = min(ambientH-1, max(0, y+d))
					}
					p := px[yy*ambientW+xx]
					sum[0], sum[1], sum[2], n = sum[0]+p[0], sum[1]+p[1], sum[2]+p[2], n+1
				}
				out[y*ambientW+x] = [3]float64{sum[0] / n, sum[1] / n, sum[2] / n}
			}
		}
		px = out
	}
	for range 3 {
		box(true)
		box(false)
	}
	img := image.NewNRGBA(image.Rect(0, 0, ambientW, ambientH))
	for i, p := range px {
		img.Set(i%ambientW, i/ambientW, color.NRGBA{uint8(p[0]), uint8(p[1]), uint8(p[2]), 255})
	}
	return img
}
