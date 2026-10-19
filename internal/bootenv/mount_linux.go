package bootenv

import (
	"fmt"
	"os"
	"syscall"
)

// WithMounted mounts dev, runs fn on the mount point and unmounts it again.
func WithMounted(dev, fstype string, fn func(dir string) error) error {
	dir, err := os.MkdirTemp("", "sc-mnt-")
	if err != nil {
		return err
	}
	defer os.Remove(dir)
	if err := syscall.Mount(dev, dir, fstype, 0, ""); err != nil {
		return fmt.Errorf("bootenv: mount %s: %w", dev, err)
	}
	ferr := fn(dir)
	syscall.Sync()
	if err := syscall.Unmount(dir, 0); err != nil && ferr == nil {
		ferr = err
	}
	return ferr
}
