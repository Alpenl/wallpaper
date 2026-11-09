// Small real-device acceptance utility; no wallpaper or fake surface is created.
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/png"
	"os"
)

func main() {
	permission := flag.Bool("permission", false, "check screen recording permission")
	window := flag.Uint64("window", 0, "fail if native window still exists")
	flag.Parse()
	var err error
	if *permission {
		err = permissionCheck()
	} else if *window != 0 {
		err = windowGone(uintptr(*window))
	} else if flag.NArg() == 2 {
		err = compare(flag.Arg(0), flag.Arg(1))
	} else {
		err = fmt.Errorf("expected two PNGs, -permission or -window")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func read(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	im, _, err := image.Decode(f)
	return im, err
}
func compare(a, b string) error {
	x, err := read(a)
	if err != nil {
		return err
	}
	y, err := read(b)
	if err != nil {
		return err
	}
	if x.Bounds() != y.Bounds() {
		return fmt.Errorf("capture geometry changed")
	}
	bounds := x.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 100 || h < 100 {
		return fmt.Errorf("capture too small")
	}
	// Exclude menu/task bars, dock and corners; a clock/cursor must not prove video.
	changed, total, nonblack := 0, 0, 0
	for py := h / 5; py < h*4/5; py += 3 {
		for px := w / 5; px < w*4/5; px += 3 {
			r, g, b, _ := x.At(px, py).RGBA()
			rr, gg, bb, _ := y.At(px, py).RGBA()
			d := abs(int(r)-int(rr)) + abs(int(g)-int(gg)) + abs(int(b)-int(bb))
			if d > 3*8*257 {
				changed++
			}
			if r+g+b > 3*8*257 {
				nonblack++
			}
			total++
		}
	}
	if nonblack < total/100 {
		return fmt.Errorf("BLOCKED: capture is black or desktop content unavailable")
	}
	fraction := float64(changed) / float64(total)
	fmt.Printf("changed_pixel_fraction=%.6f\n", fraction)
	if fraction < 0.005 {
		return fmt.Errorf("no significant desktop motion (less than 0.5%% changed pixels)")
	}
	return nil
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
