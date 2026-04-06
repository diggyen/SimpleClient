//go:build linux

package framebuffer

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"syscall"
	"unsafe"
)

// FBIOGET_VSCREENINFO is the ioctl command to read variable screen info.
const FBIOGET_VSCREENINFO = 0x4600

// FB represents an open framebuffer device.
type FB struct {
	file   *os.File
	data   []byte
	width  int
	height int
	stride int // bytes per row
}

// Open opens the framebuffer device at path (typically /dev/fb0), reads
// screen info via ioctl, and maps the framebuffer into memory.
func Open(path string) (*FB, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	var vinfo fbVarScreenInfo
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		FBIOGET_VSCREENINFO,
		uintptr(unsafe.Pointer(&vinfo)),
	); errno != 0 {
		f.Close()
		return nil, fmt.Errorf("ioctl FBIOGET_VSCREENINFO: %w", errno)
	}

	w := int(vinfo.Xres)
	h := int(vinfo.Yres)
	bytesPerPixel := int(vinfo.BitsPerPixel) / 8
	stride := w * bytesPerPixel
	size := stride * h

	data, err := syscall.Mmap(
		int(f.Fd()),
		0,
		size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED,
	)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap framebuffer: %w", err)
	}

	return &FB{
		file:   f,
		data:   data,
		width:  w,
		height: h,
		stride: stride,
	}, nil
}

// Bounds returns the framebuffer resolution as a rectangle.
func (fb *FB) Bounds() image.Rectangle {
	return image.Rect(0, 0, fb.width, fb.height)
}

// Width returns the horizontal resolution.
func (fb *FB) Width() int { return fb.width }

// Height returns the vertical resolution.
func (fb *FB) Height() int { return fb.height }

// Blit copies an RGBA image to the framebuffer, converting RGBA → BGRA.
// The image is clipped to the framebuffer bounds automatically.
func (fb *FB) Blit(img *image.RGBA) {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y && y < fb.height; y++ {
		for x := bounds.Min.X; x < bounds.Max.X && x < fb.width; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			off := y*fb.stride + x*4
			if off+3 >= len(fb.data) {
				continue
			}
			fb.data[off+0] = byte(b >> 8)
			fb.data[off+1] = byte(g >> 8)
			fb.data[off+2] = byte(r >> 8)
			fb.data[off+3] = 0xff
		}
	}
}

// BlitRect copies only the sub-rectangle r of img to the framebuffer.
func (fb *FB) BlitRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(image.Rect(0, 0, fb.width, fb.height))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.RGBAAt(x, y)
			off := y*fb.stride + x*4
			fb.data[off+0] = c.B
			fb.data[off+1] = c.G
			fb.data[off+2] = c.R
			fb.data[off+3] = 0xff
		}
	}
}

// WritePixel writes a single pixel directly to the framebuffer.
func (fb *FB) WritePixel(x, y int, c color.RGBA) {
	if x < 0 || x >= fb.width || y < 0 || y >= fb.height {
		return
	}
	off := y*fb.stride + x*4
	fb.data[off+0] = c.B
	fb.data[off+1] = c.G
	fb.data[off+2] = c.R
	fb.data[off+3] = 0xff
}

// Close unmaps the framebuffer and closes the device file.
func (fb *FB) Close() error {
	_ = syscall.Munmap(fb.data)
	return fb.file.Close()
}
