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

// FBIOGET_FSCREENINFO is the ioctl command to read fixed screen info.
const FBIOGET_FSCREENINFO = 0x4602

// FB represents an open framebuffer device.
type FB struct {
	file   *os.File
	data   []byte
	width  int
	height int
	stride int // bytes per row, as the driver lays them out
	bpp    int // bytes per pixel

	// Channel positions from the driver. The common case — 32 bits with blue
	// in the lowest byte, which is what simpledrm, efifb and vesafb report on
	// every PC — gets a fast path; anything else is packed field by field.
	rOff, gOff, bOff uint32
	rLen, gLen, bLen uint32
	xrgb             bool

	// row is the scratch line a blit is converted into before being copied to
	// the mapping in one go. Framebuffer memory is uncached write-combining on
	// real hardware, where four scattered byte stores per pixel cost several
	// times what one sequential copy of the finished row does.
	row []byte
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
	switch bytesPerPixel {
	case 2, 3, 4:
	default:
		f.Close()
		return nil, fmt.Errorf("framebuffer %s: unsupported depth %d bpp", path, vinfo.BitsPerPixel)
	}

	// The row pitch has to come from the driver. width*bpp is right at
	// 1024x768, but a 1366- or 1600-wide mode is commonly padded to an aligned
	// pitch, and assuming otherwise shears the whole picture diagonally.
	stride := w * bytesPerPixel
	var finfo fbFixScreenInfo
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		FBIOGET_FSCREENINFO,
		uintptr(unsafe.Pointer(&finfo)),
	); errno == 0 && int(finfo.LineLength) >= stride {
		stride = int(finfo.LineLength)
	}
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

	fb := &FB{
		file:   f,
		data:   data,
		width:  w,
		height: h,
		stride: stride,
		bpp:    bytesPerPixel,
		rOff:   vinfo.Red.Offset, rLen: vinfo.Red.Length,
		gOff: vinfo.Green.Offset, gLen: vinfo.Green.Length,
		bOff: vinfo.Blue.Offset, bLen: vinfo.Blue.Length,
		row: make([]byte, stride),
	}
	// A driver that reports no channel layout at all is treated as the usual
	// XRGB8888 / RGB565 rather than painting black.
	if fb.rLen == 0 || fb.gLen == 0 || fb.bLen == 0 {
		if bytesPerPixel == 2 {
			fb.rOff, fb.rLen, fb.gOff, fb.gLen, fb.bOff, fb.bLen = 11, 5, 5, 6, 0, 5
		} else {
			fb.rOff, fb.rLen, fb.gOff, fb.gLen, fb.bOff, fb.bLen = 16, 8, 8, 8, 0, 8
		}
	}
	fb.xrgb = bytesPerPixel == 4 &&
		fb.rOff == 16 && fb.gOff == 8 && fb.bOff == 0 &&
		fb.rLen == 8 && fb.gLen == 8 && fb.bLen == 8
	return fb, nil
}

// Bounds returns the framebuffer resolution as a rectangle.
func (fb *FB) Bounds() image.Rectangle {
	return image.Rect(0, 0, fb.width, fb.height)
}

// Width returns the horizontal resolution.
func (fb *FB) Width() int { return fb.width }

// Height returns the vertical resolution.
func (fb *FB) Height() int { return fb.height }

// Blit copies an RGBA image to the framebuffer, clipped to its bounds, and
// puts it on the screen at once.
func (fb *FB) Blit(img *image.RGBA) {
	fb.BlitRect(img, img.Bounds())
	fb.Flush()
}

// Flush puts what has been written on the screen now. The framebuffer a GPU
// driver emulates (nouveau, amdgpu and radeon set up drm_fbdev_ttm) is a
// shadow in system memory that the kernel copies to the screen 50 ms after
// the first write to it, which held the kiosk's own screens to 20 frames a
// second while the pointer moved; fsync has it copied at once. A firmware
// framebuffer has no shadow, and fsync returns straight away.
func (fb *FB) Flush() {
	_ = fb.file.Sync()
}

// BlitRect copies only the sub-rectangle r of img to the framebuffer.
func (fb *FB) BlitRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(image.Rect(0, 0, fb.width, fb.height)).Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	n := r.Dx()
	line := fb.row[:n*fb.bpp]
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := img.Pix[img.PixOffset(r.Min.X, y):][:n*4]
		fb.packRow(line, src)
		off := y*fb.stride + r.Min.X*fb.bpp
		copy(fb.data[off:off+len(line)], line)
	}
}

// packRow converts one row of RGBA pixels into the framebuffer's format.
func (fb *FB) packRow(dst, src []byte) {
	if fb.xrgb {
		for s, d := 0, 0; s < len(src); s, d = s+4, d+4 {
			dst[d+0] = src[s+2]
			dst[d+1] = src[s+1]
			dst[d+2] = src[s+0]
			dst[d+3] = 0xff
		}
		return
	}
	for s, d := 0, 0; s < len(src); s, d = s+4, d+fb.bpp {
		v := fb.pack(src[s], src[s+1], src[s+2])
		switch fb.bpp {
		case 2:
			dst[d], dst[d+1] = byte(v), byte(v>>8)
		case 3:
			dst[d], dst[d+1], dst[d+2] = byte(v), byte(v>>8), byte(v>>16)
		case 4:
			dst[d], dst[d+1], dst[d+2], dst[d+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		}
	}
}

// pack places 8-bit channels at the driver's offsets and widths.
func (fb *FB) pack(r, g, b uint8) uint32 {
	return scale(r, fb.rLen)<<fb.rOff | scale(g, fb.gLen)<<fb.gOff | scale(b, fb.bLen)<<fb.bOff
}

// scale widens or narrows an 8-bit channel to n bits; 10-bit (deep colour)
// firmware modes exist, so wider than 8 has to work too.
func scale(v uint8, n uint32) uint32 {
	if n <= 8 {
		return uint32(v) >> (8 - n)
	}
	return uint32(v) << (n - 8)
}

// WritePixel writes a single pixel directly to the framebuffer.
func (fb *FB) WritePixel(x, y int, c color.RGBA) {
	if x < 0 || x >= fb.width || y < 0 || y >= fb.height {
		return
	}
	off := y*fb.stride + x*fb.bpp
	if fb.xrgb {
		fb.data[off+0] = c.B
		fb.data[off+1] = c.G
		fb.data[off+2] = c.R
		fb.data[off+3] = 0xff
		return
	}
	fb.packRow(fb.data[off:off+fb.bpp], []byte{c.R, c.G, c.B, 0xff})
}

// Close unmaps the framebuffer and closes the device file.
func (fb *FB) Close() error {
	_ = syscall.Munmap(fb.data)
	return fb.file.Close()
}
