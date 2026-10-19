//go:build linux

package framebuffer

// fbBitfield mirrors the Linux fb_bitfield struct.
type fbBitfield struct {
	Offset   uint32
	Length   uint32
	MsbRight uint32
}

// fbVarScreenInfo mirrors the Linux fb_var_screeninfo struct (160 bytes).
// See: /usr/include/linux/fb.h
type fbVarScreenInfo struct {
	Xres         uint32
	Yres         uint32
	XresVirtual  uint32
	YresVirtual  uint32
	Xoffset      uint32
	Yoffset      uint32
	BitsPerPixel uint32
	Grayscale    uint32
	Red          fbBitfield
	Green        fbBitfield
	Blue         fbBitfield
	Transp       fbBitfield
	Nonstd       uint32
	Activate     uint32
	Height       uint32
	Width        uint32
	AccelFlags   uint32
	Pixclock     uint32
	LeftMargin   uint32
	RightMargin  uint32
	UpperMargin  uint32
	LowerMargin  uint32
	HsyncLen     uint32
	VsyncLen     uint32
	Sync         uint32
	Vmode        uint32
	Rotate       uint32
	Colorspace   uint32
	Reserved     [4]uint32
}

// fbFixScreenInfo mirrors the Linux fb_fix_screeninfo struct (80 bytes on
// 64-bit). Only LineLength is used: the driver's real row pitch, which is not
// always width*bytesPerPixel.
type fbFixScreenInfo struct {
	ID           [16]byte
	SmemStart    uint64
	SmemLen      uint32
	Type         uint32
	TypeAux      uint32
	Visual       uint32
	Xpanstep     uint16
	Ypanstep     uint16
	Ywrapstep    uint16
	_            uint16
	LineLength   uint32
	MmioStart    uint64
	MmioLen      uint32
	Accel        uint32
	Capabilities uint16
	Reserved     [2]uint16
}
