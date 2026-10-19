//go:build !linux

package usb

import "errors"

var errNotLinux = errors.New("usb: mounting is only supported on Linux")

func sysMount(node, dir, fstype, opts string, ro bool) error { return errNotLinux }

func sysUnmount(dir string) error { return errNotLinux }

func sysSync() {}
