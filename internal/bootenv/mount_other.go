//go:build !linux

package bootenv

import "errors"

// WithMounted is Linux-only; elsewhere there is nothing to mount.
func WithMounted(dev, fstype string, fn func(dir string) error) error {
	return errors.New("bootenv: mounting is only supported on Linux")
}
