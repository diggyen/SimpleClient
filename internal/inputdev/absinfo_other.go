//go:build !linux

package inputdev

import "os"

// readAbsRange has no ioctl to call off Linux; tests treat the axis as unranged.
func readAbsRange(*os.File, int) absRange { return absRange{} }
