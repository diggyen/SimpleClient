//go:build linux

package inputdev

import (
	"os"
	"syscall"
	"unsafe"
)

// readAbsRange asks the driver for an axis's range (EVIOCGABS).
func readAbsRange(f *os.File, axis int) absRange {
	var info struct {
		Value, Minimum, Maximum, Fuzz, Flat, Resolution int32
	}
	req := uintptr(0x80184540 + axis) // _IOR('E', 0x40+axis, struct input_absinfo)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(unsafe.Pointer(&info))); errno != 0 {
		return absRange{}
	}
	return absRange{info.Minimum, info.Maximum}
}
