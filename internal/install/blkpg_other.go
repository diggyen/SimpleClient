//go:build !linux

package install

import (
	"errors"
	"os"

	"github.com/diggyen/SimpleClient/internal/gpt"
)

// addPartition is Linux-only; elsewhere there is no kernel to tell.
func addPartition(f *os.File, p gpt.Partition) error {
	return errors.New("install: adding partitions is only supported on Linux")
}
