package install

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"github.com/diggyen/SimpleClient/internal/gpt"
)

// BLKPG, from <linux/blkpg.h>.
const (
	blkpg             = 0x1269
	blkpgAddPartition = 1
)

type blkpgPartition struct {
	start, length int64 // bytes
	pno           int32
	devname       [64]byte
	volname       [64]byte
}

type blkpgArg struct {
	op, flags, datalen int32
	data               unsafe.Pointer
}

// addPartition tells the kernel of partition p, just added to the table of
// disk f, the way partx does. One it knows already is no error.
func addPartition(f *os.File, p gpt.Partition) error {
	part := blkpgPartition{start: p.Start * gpt.SectorSize, length: (p.End - p.Start + 1) * gpt.SectorSize, pno: int32(p.Num)}
	arg := blkpgArg{op: blkpgAddPartition, datalen: int32(unsafe.Sizeof(part)), data: unsafe.Pointer(&part)}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), blkpg, uintptr(unsafe.Pointer(&arg)))
	if e != 0 && !errors.Is(e, syscall.EBUSY) {
		return &os.PathError{Op: "BLKPG_ADD_PARTITION", Path: f.Name(), Err: e}
	}
	return nil
}
