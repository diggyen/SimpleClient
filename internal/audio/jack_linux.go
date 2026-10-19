//go:build linux

package audio

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	swHeadphoneInsert = 0x02
	swLineoutInsert   = 0x06
	// EVIOCGSW(8): _IOC(_IOC_READ, 'E', 0x1b, 8)
	eviocgsw8 = 0x8008451b
)

func init() { headphonesIn = jackPlugged }

// jackPlugged reports whether headphones (or a line-out cable) are in the
// board's jack. HD Audio codecs expose the jack as an input device whose
// switch state the kernel keeps current.
func jackPlugged() bool {
	data, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return false
	}
	for _, block := range strings.Split(string(data), "\n\n") {
		if !strings.Contains(block, "Headphone") && !strings.Contains(block, "Line Out") {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "H: Handlers=") {
				continue
			}
			for _, h := range strings.Fields(strings.TrimPrefix(line, "H: Handlers=")) {
				if strings.HasPrefix(h, "event") && switchOn("/dev/input/"+h) {
					return true
				}
			}
		}
	}
	return false
}

func switchOn(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var bits uint64
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), eviocgsw8, uintptr(unsafe.Pointer(&bits))); errno != 0 {
		return false
	}
	return bits&(1<<swHeadphoneInsert) != 0 || bits&(1<<swLineoutInsert) != 0
}
