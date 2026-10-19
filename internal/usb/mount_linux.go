package usb

import "syscall"

// sysMount mounts a drive for the server's files and nothing else: no
// programs run from it, no device nodes or setuid bits count on it, and
// reading does not write access times back to a flash chip.
func sysMount(node, dir, fstype, opts string, ro bool) error {
	flags := uintptr(syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC | syscall.MS_NOATIME)
	if ro {
		flags |= syscall.MS_RDONLY
	}
	return syscall.Mount(node, dir, fstype, flags, opts)
}

func sysUnmount(dir string) error {
	return syscall.Unmount(dir, syscall.MNT_DETACH)
}

// sysSync writes out every file system's dirty data. The drives are the
// only file systems a kiosk writes to, so there is nothing else to wait on.
func sysSync() {
	syscall.Sync()
}
