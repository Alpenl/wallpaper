//go:build windows

package web

import (
	"bytes"
	"fmt"
	"image/png"
	"syscall"
	"unsafe"
)

// CapturePreview snapshots the compositor before hiding it. A plain STATIC
// child displays the decoded frame while WebView2 is suspended; no browser code
// is needed to keep the last frame on screen.
func (w *winView) capture() error {
	var stream uintptr
	r, _, _ := ole.NewProc("CreateStreamOnHGlobal").Call(0, 1, ptr(&stream))
	if e := hr(r); e != nil {
		return e
	}
	defer release(stream)
	done := false
	var result error
	callback := handler(func(hrCode, _ uintptr) uintptr { result = hr(hrCode); done = true; return 0 })
	defer release(callback)
	if e := hr(com(w.wv, 30, 0, stream, callback)); e != nil {
		return e
	}
	if e := await(&done); e != nil {
		return e
	}
	if result != nil {
		return result
	}
	var global uintptr
	r, _, _ = ole.NewProc("GetHGlobalFromStream").Call(stream, ptr(&global))
	if e := hr(r); e != nil {
		return e
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	size, _, _ := kernel.NewProc("GlobalSize").Call(global)
	if size == 0 || size > 256<<20 {
		return fmt.Errorf("web: snapshot size limit")
	}
	mem, _, _ := kernel.NewProc("GlobalLock").Call(global)
	if mem == 0 {
		return fmt.Errorf("web: snapshot lock failed")
	}
	raw := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(mem)), int(size))...)
	kernel.NewProc("GlobalUnlock").Call(global)
	frame, e := png.Decode(bytes.NewReader(raw))
	if e != nil {
		return e
	}
	b := frame.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 || int64(width)*int64(height) > 64<<20 {
		return fmt.Errorf("web: snapshot dimensions invalid")
	}
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, ImageSize uint32
		XPels, YPels           int32
		Used, Important        uint32
	}{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, Bits: 32}
	var bits uintptr
	gdi := syscall.NewLazyDLL("gdi32.dll")
	bitmap, _, _ := gdi.NewProc("CreateDIBSection").Call(0, ptr(&header), 0, ptr(&bits), 0, 0)
	if bitmap == 0 || bits == 0 {
		return fmt.Errorf("web: snapshot bitmap failed")
	}
	pixels := unsafe.Slice((*byte)(unsafe.Pointer(bits)), width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, a := frame.At(x+b.Min.X, y+b.Min.Y).RGBA()
			i := (y*width + x) * 4
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = byte(b>>8), byte(g>>8), byte(r>>8), byte(a>>8)
		}
	}
	if w.snapshotWindow == 0 {
		w.snapshotWindow, _, _ = user.NewProc("CreateWindowExW").Call(0, wide("STATIC"), 0, 0x40000000|0xE, 0, 0, uintptr(width), uintptr(height), w.parent, 0, 0, 0)
	}
	if w.snapshotWindow == 0 {
		gdi.NewProc("DeleteObject").Call(bitmap)
		return fmt.Errorf("web: snapshot window failed")
	}
	user.NewProc("SendMessageW").Call(w.snapshotWindow, 0x0172, 0, bitmap)
	if w.snapshotBitmap != 0 {
		gdi.NewProc("DeleteObject").Call(w.snapshotBitmap)
	}
	w.snapshotBitmap = bitmap
	user.NewProc("SetWindowPos").Call(w.snapshotWindow, 0, 0, 0, uintptr(width), uintptr(height), 0x0010|0x0040)
	return nil
}
func (w *winView) clearSnapshot() {
	if w.snapshotWindow != 0 {
		user.NewProc("DestroyWindow").Call(w.snapshotWindow)
		w.snapshotWindow = 0
	}
	if w.snapshotBitmap != 0 {
		syscall.NewLazyDLL("gdi32.dll").NewProc("DeleteObject").Call(w.snapshotBitmap)
		w.snapshotBitmap = 0
	}
}
