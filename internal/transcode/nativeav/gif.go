package nativeav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"os"
)

// preflightGIF bounds every allocation before image/gif.DecodeAll is allowed
// to run. Checking only DecodeConfig is insufficient: many small compressed
// frames can otherwise allocate an unbounded sum of decoded pixel buffers.
func preflightGIF(ctx context.Context, b []byte) error {
	if len(b) > maxGIFBytes {
		return errors.New("nativeav: GIF exceeds 64 MiB input limit")
	}
	if len(b) < 13 || (string(b[:6]) != "GIF87a" && string(b[:6]) != "GIF89a") {
		return errors.New("nativeav: invalid GIF header")
	}
	if b[12] != 0 && b[12] != 49 {
		return errors.New("nativeav: GIF non-square pixel aspect ratios are unsupported")
	}
	u16 := func(p []byte) int { return int(binary.LittleEndian.Uint16(p)) }
	w, h := u16(b[6:]), u16(b[8:])
	if w < 2 || h < 2 || w > maxDimension || h > maxDimension || int64(w)*int64(h) > maxCanvasPixels {
		return errors.New("nativeav: GIF canvas exceeds dimension/pixel limits")
	}
	pos := 13
	skip := func(n int) bool {
		if n < 0 || n > len(b)-pos {
			return false
		}
		pos += n
		return true
	}
	if b[10]&0x80 != 0 && !skip(3*(1<<((b[10]&7)+1))) {
		return io.ErrUnexpectedEOF
	}
	subblocks := func() error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			if pos >= len(b) {
				return io.ErrUnexpectedEOF
			}
			n := int(b[pos])
			pos++
			if n == 0 {
				return nil
			}
			if !skip(n) {
				return io.ErrUnexpectedEOF
			}
		}
	}
	var frames, pixels, duration int64
	delay := 0
	for pos < len(b) {
		if err := ctx.Err(); err != nil {
			return err
		}
		tag := b[pos]
		pos++
		switch tag {
		case 0x3b:
			if frames == 0 {
				return errors.New("nativeav: GIF has no images")
			}
			return nil
		case 0x21:
			if pos >= len(b) {
				return io.ErrUnexpectedEOF
			}
			label := b[pos]
			pos++
			if label == 0xf9 {
				if len(b)-pos < 6 {
					return io.ErrUnexpectedEOF
				}
				if b[pos] != 4 || b[pos+5] != 0 {
					return errors.New("nativeav: invalid GIF control extension")
				}
				disposal := (b[pos+1] >> 2) & 7
				if disposal > 3 {
					return errors.New("nativeav: unsupported GIF disposal method")
				}
				delay = u16(b[pos+2:])
				pos += 6
			} else {
				if label == 0x01 {
					return errors.New("nativeav: GIF plain-text rendering is unsupported")
				}
				if err := subblocks(); err != nil {
					return err
				}
			}
		case 0x2c:
			if len(b)-pos < 9 {
				return io.ErrUnexpectedEOF
			}
			x, y, fw, fh := u16(b[pos:]), u16(b[pos+2:]), u16(b[pos+4:]), u16(b[pos+6:])
			packed := b[pos+8]
			pos += 9
			if fw < 1 || fh < 1 || x+fw > w || y+fh > h {
				return errors.New("nativeav: GIF frame is outside logical canvas")
			}
			frames++
			pixels += int64(fw) * int64(fh)
			duration += int64(gifDelay(delay))
			delay = 0
			if frames > maxGIFFrames || pixels > maxGIFDecodedPixels || duration > maxDurationSeconds*100 {
				return errors.New("nativeav: GIF decoded frames/pixels/duration budget exceeded")
			}
			if packed&0x80 != 0 && !skip(3*(1<<((packed&7)+1))) {
				return io.ErrUnexpectedEOF
			}
			if !skip(1) {
				return io.ErrUnexpectedEOF
			} // LZW minimum code size; decoder validates it.
			if err := subblocks(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("nativeav: invalid GIF block 0x%02x", tag)
		}
	}
	return io.ErrUnexpectedEOF
}

type contextReader struct {
	ctx context.Context
	*bytes.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
func (r contextReader) ReadByte() (byte, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.ReadByte()
}

func loadGIF(ctx context.Context, path string) (*gif.GIF, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxGIFBytes+1))
	if err != nil {
		return nil, err
	}
	if err = preflightGIF(ctx, b); err != nil {
		return nil, err
	}
	g, err := gif.DecodeAll(contextReader{ctx, bytes.NewReader(b)})
	if err != nil {
		return nil, fmt.Errorf("nativeav: decode GIF: %w", err)
	}
	return g, nil
}

// gifFrames incrementally composites subrectangles and disposal methods onto a
// single logical canvas. Frames are never cached as full-canvas RGBA snapshots.
// Transparent pixels preserve existing contents. An opaque GIF global background
// is used for opaque frames. Frames with a transparency entry restore to clear,
// matching transparent animation compositing; the H.264 writer flattens clear
// pixels onto black. Local palette indices never redefine the global background.
type gifFrames struct {
	gif              *gif.GIF
	canvas, previous *image.RGBA
	index            int
	endCS            int64
}

func newGIFFrames(g *gif.GIF) (*gifFrames, error) {
	if g == nil || len(g.Image) == 0 || len(g.Delay) != len(g.Image) || g.Config.Width < 2 || g.Config.Height < 2 || int64(g.Config.Width)*int64(g.Config.Height) > maxCanvasPixels {
		return nil, errors.New("nativeav: invalid decoded GIF")
	}
	s := &gifFrames{gif: g, index: -1, canvas: image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))}
	draw.Draw(s.canvas, s.canvas.Bounds(), image.NewUniform(s.background(0)), image.Point{}, draw.Src)
	return s, nil
}
func (s *gifFrames) background(index int) color.Color {
	bg := int(s.gif.BackgroundIndex)
	if index >= 0 && index < len(s.gif.Image) {
		for _, c := range s.gif.Image[index].Palette {
			_, _, _, a := c.RGBA()
			if a == 0 {
				return color.Transparent
			}
		}
	}
	if p, ok := s.gif.Config.ColorModel.(color.Palette); ok && bg < len(p) {
		return p[bg]
	}
	return color.Transparent
}
func (s *gifFrames) disposal(i int) byte {
	if i >= 0 && i < len(s.gif.Disposal) {
		return s.gif.Disposal[i]
	}
	return 0
}
func (s *gifFrames) duration() int64 {
	var n int64
	for _, d := range s.gif.Delay {
		n += int64(gifDelay(d))
	}
	return n
}
func (s *gifFrames) at(ctx context.Context, frame int) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if frame < 0 {
		return nil, errors.New("nativeav: negative frame index")
	}
	// Compare centiseconds against exact rational frame/30, not rounded ms.
	for s.index < 0 || int64(frame)*100 >= s.endCS*FPS {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := s.index + 1
		if next >= len(s.gif.Image) {
			return s.canvas, nil
		}
		if s.index >= 0 {
			switch s.disposal(s.index) {
			case gif.DisposalBackground:
				draw.Draw(s.canvas, s.gif.Image[s.index].Bounds(), image.NewUniform(s.background(s.index)), image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				if s.previous != nil {
					copy(s.canvas.Pix, s.previous.Pix)
				}
			}
		}
		if s.disposal(next) == gif.DisposalPrevious {
			if s.previous == nil {
				s.previous = image.NewRGBA(s.canvas.Bounds())
			}
			copy(s.previous.Pix, s.canvas.Pix)
		}
		im := s.gif.Image[next]
		draw.Draw(s.canvas, im.Bounds(), im, im.Bounds().Min, draw.Over)
		s.index = next
		s.endCS += int64(gifDelay(s.gif.Delay[next]))
	}
	return s.canvas, nil
}
