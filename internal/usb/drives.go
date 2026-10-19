package usb

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Where drives are found and mounted; tests point these elsewhere.
var (
	sysBlock = "/sys/block"
	devDir   = "/dev"
	mediaDir = "/media"
)

// isMedium reports whether a disk is the SimpleClient ISO the kiosk was
// booted from; set by the caller (install.IsInstallMedium), which knows the
// image's volume ID.
var isMedium = func(dev string) bool { return false }

// SetMediumCheck tells the drive watcher how to recognise the boot medium.
func SetMediumCheck(f func(dev string) bool) { isMedium = f }

// The mount and probe operations, separate so tests can stand in for them.
var (
	mountFS   = sysMount
	unmountFS = sysUnmount
	syncFS    = sysSync
	probeFS   = blkid
)

// filesystem is how one kind of file system is mounted.
type filesystem struct {
	fstype string // the kernel's name for it
	opts   string
	ro     bool // read-only by nature
}

// filesystems are the ones a drive may carry, by blkid's name. FAT, exFAT
// and NTFS have no owners, so everything on them is root's: the engine reads
// and writes as root.
var filesystems = map[string]filesystem{
	"vfat":    {"vfat", "uid=0,gid=0,fmask=0133,dmask=0022,iocharset=utf8,shortname=mixed,flush,errors=remount-ro", false},
	"exfat":   {"exfat", "uid=0,gid=0,fmask=0133,dmask=0022,iocharset=utf8,errors=remount-ro", false},
	"ntfs":    {"ntfs3", "uid=0,gid=0,fmask=0133,dmask=0022,iocharset=utf8", false},
	"ext2":    {"ext4", "errors=remount-ro", false},
	"ext3":    {"ext4", "errors=remount-ro", false},
	"ext4":    {"ext4", "errors=remount-ro", false},
	"iso9660": {"iso9660", "iocharset=utf8", true},
}

// Drives keeps the kiosk's USB drives and SD cards mounted under /media,
// which is where the session engine's drive redirection finds them: FreeRDP
// reads /proc/mounts every second and offers the server every mount one level
// under /media, under the directory's name. A drive pulled out is unmounted,
// and the server told it has gone. The medium the kiosk boots from, and a
// disk SimpleClient is installed on, are never touched.
type Drives struct {
	mu     sync.Mutex
	held   map[string]bool    // disks being installed to: left alone
	mounts map[string]*drive  // by block device, e.g. "sdb1"
	failed map[string]failure // block devices that would not mount
	own    map[string]verdict // disk -> whether it is SimpleClient's, for its medium
}

type drive struct {
	dev    string // block device, e.g. "sdb1"
	disk   string // the disk it is on, e.g. "sdb"
	medium string // the disk's medium when mounted: a card swapped in the same slot is another
	dir    string // e.g. "/media/KINGSTON"
}

// failure counts the attempts at mounting one medium. A drive is tried a few
// times — a stick can still be settling a moment after its partitions show —
// and then left alone until another medium is in.
type failure struct {
	medium string
	n      int
}

const mountAttempts = 3

// verdict is whether a disk is SimpleClient's, for the medium and partitions it
// had when looked at: partitions show up a moment after the disk does.
type verdict struct {
	key string
	own bool
}

// NewDrives returns a watcher with nothing mounted yet.
func NewDrives() *Drives {
	return &Drives{
		held:   map[string]bool{},
		mounts: map[string]*drive{},
		failed: map[string]failure{},
		own:    map[string]verdict{},
	}
}

// Run watches for drives for ever.
func (d *Drives) Run() {
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		log.Printf("usb: %v", err)
		return
	}
	go d.syncLoop()
	for {
		d.Poll()
		time.Sleep(time.Second)
	}
}

// Hold unmounts disk's drives and leaves it alone from now on: the installer
// is about to write over it.
func (d *Drives) Hold(disk string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held[disk] = true
	for dev, m := range d.mounts {
		if m.disk == disk {
			d.unmount(dev, m)
		}
	}
}

// Poll mounts the drives that have appeared and unmounts the ones that have
// gone.
func (d *Drives) Poll() {
	want := d.present()

	d.mu.Lock()
	defer d.mu.Unlock()
	for dev, m := range d.mounts {
		if want[dev] != m.medium || d.held[m.disk] {
			d.unmount(dev, m)
		}
	}
	for dev, medium := range want {
		if _, ok := d.mounts[dev]; ok {
			continue
		}
		f := d.failed[dev]
		if f.medium != medium {
			f = failure{medium: medium}
		}
		if f.n >= mountAttempts {
			continue
		}
		disk := diskOf(dev)
		if d.held[disk] {
			continue
		}
		if err := d.mount(dev, disk, medium); err != nil {
			f.n++
			d.failed[dev] = f
			if f.n == mountAttempts {
				log.Printf("usb: %s: %v", dev, err)
			}
		} else {
			delete(d.failed, dev)
		}
	}
}

// present lists the block devices that should be mounted now, each with the
// medium it is on.
func (d *Drives) present() map[string]string {
	want := map[string]string{}
	disks, err := os.ReadDir(sysBlock)
	if err != nil {
		return want
	}
	for _, e := range disks {
		disk := e.Name()
		if !removable(disk) {
			continue
		}
		medium := mediumOf(disk)
		if medium == "" {
			continue
		}
		devs := partitions(disk)
		if len(devs) == 0 {
			// A drive formatted without a partition table.
			devs = []string{disk}
		}
		if d.ownDisk(disk, medium, devs) {
			continue
		}
		for _, dev := range devs {
			want[dev] = medium
		}
	}
	return want
}

// ownDisk reports whether a disk is SimpleClient's: the ISO on a USB stick, or
// a disk it is installed on, which has partitions labelled SC_.
func (d *Drives) ownDisk(disk, medium string, devs []string) bool {
	key := medium + " " + strings.Join(devs, " ")
	d.mu.Lock()
	v, known := d.own[disk]
	d.mu.Unlock()
	if known && v.key == key {
		return v.own
	}
	own := isMedium(filepath.Join(devDir, disk))
	for _, dev := range devs {
		if _, label := probeFS(filepath.Join(devDir, dev)); strings.HasPrefix(label, "SC_") {
			own = true
		}
	}
	d.mu.Lock()
	d.own[disk] = verdict{key: key, own: own}
	d.mu.Unlock()
	return own
}

// removable reports whether a disk is one to mount: a USB drive (a stick, a
// card reader, a hard disk in an enclosure) or an SD card. A drive inside a
// printer is the printer's, and goes with it; the machine's own disks are
// not the server's business.
func removable(disk string) bool {
	path, err := filepath.EvalSymlinks(filepath.Join(sysBlock, disk))
	if err != nil {
		return false
	}
	if strings.HasPrefix(disk, "mmcblk") {
		return attr(filepath.Join(path, "device"), "type") == "SD"
	}
	busID := usbDeviceOf(path)
	if busID == "" {
		return false
	}
	dev, ok := readDevice(busID)
	return ok && dev.Kind() == Storage
}

// usbDeviceOf is the USB device a sysfs path lies under: the last component
// that is a bus ID, so a hub's port is passed over for the device behind it.
func usbDeviceOf(path string) string {
	id := ""
	for _, c := range strings.Split(filepath.ToSlash(path), "/") {
		if isBusID(c) {
			id = c
		}
	}
	return id
}

// mediumOf identifies what is in a drive now: the kernel numbers each medium
// a disk is given (diskseq), so a card taken out and another put in reads
// differently even between two looks. Empty when the slot is empty.
func mediumOf(disk string) string {
	dir := filepath.Join(sysBlock, disk)
	size := attr(dir, "size")
	if size == "" || size == "0" {
		return ""
	}
	if seq := attr(dir, "diskseq"); seq != "" {
		return seq
	}
	return attr(dir, "dev") + "/" + size
}

// partitions lists a disk's partitions, e.g. sdb1 and sdb2 for sdb.
func partitions(disk string) []string {
	entries, err := os.ReadDir(filepath.Join(sysBlock, disk))
	if err != nil {
		return nil
	}
	var parts []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(sysBlock, disk, e.Name(), "partition")); err == nil {
			parts = append(parts, e.Name())
		}
	}
	return parts
}

// diskOf is the disk a block device is on: sdb for sdb1, mmcblk0 for
// mmcblk0p1, the device itself for a whole disk.
func diskOf(dev string) string {
	entries, _ := os.ReadDir(sysBlock)
	for _, e := range entries {
		if e.Name() == dev {
			return dev
		}
		if _, err := os.Stat(filepath.Join(sysBlock, e.Name(), dev, "partition")); err == nil {
			return e.Name()
		}
	}
	return dev
}

// mount probes dev and mounts it under /media. Callers hold d.mu.
func (d *Drives) mount(dev, disk, medium string) error {
	node := filepath.Join(devDir, dev)
	fsName, label := probeFS(node)
	fs, ok := filesystems[fsName]
	if !ok {
		if fsName == "" {
			return fmt.Errorf("no file system found")
		}
		return fmt.Errorf("file system %s is not supported", fsName)
	}
	dir := filepath.Join(mediaDir, d.freeName(dirName(label)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ro := fs.ro || attr(filepath.Join(sysBlock, disk), "ro") == "1"
	err := mountFS(node, dir, fs.fstype, fs.opts, ro)
	if err != nil && !ro {
		// A write-protected card, or an NTFS volume Windows left open
		// (hibernated, or shut down with fast start-up): read-only still
		// gets the files to the server.
		ro = true
		err = mountFS(node, dir, fs.fstype, fs.opts, true)
	}
	if err != nil {
		os.Remove(dir)
		return fmt.Errorf("mount %s (%s): %w", node, fsName, err)
	}
	d.mounts[dev] = &drive{dev: dev, disk: disk, medium: medium, dir: dir}
	mode := "read-write"
	if ro {
		mode = "read-only"
	}
	log.Printf("usb: drive %s (%s %q) at %s, %s", dev, fsName, label, dir, mode)
	return nil
}

// unmount takes a drive away. It has usually been pulled out already, so
// the mount is detached rather than waited on. Callers hold d.mu.
func (d *Drives) unmount(dev string, m *drive) {
	if err := unmountFS(m.dir); err != nil {
		log.Printf("usb: unmount %s: %v", m.dir, err)
	}
	os.Remove(m.dir)
	delete(d.mounts, dev)
	log.Printf("usb: drive %s gone from %s", dev, m.dir)
}

// syncLoop writes the drives' changes out every second. Nobody ejects a
// stick from a kiosk: what the server saved has to be on it by the time the
// user reaches for it.
func (d *Drives) syncLoop() {
	for {
		time.Sleep(time.Second)
		d.mu.Lock()
		n := len(d.mounts)
		d.mu.Unlock()
		if n > 0 {
			syncFS()
		}
	}
}

// dirName is the directory a drive is mounted on — which is also the name
// the server shows it by: its label, or USB for a drive without one.
// /proc/mounts escapes spaces, so they become underscores.
func dirName(label string) string {
	var b strings.Builder
	n := 0
	for _, r := range label {
		if n == 32 {
			break
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		default:
			continue
		}
		n++
	}
	if s := strings.Trim(b.String(), "._-"); s != "" {
		return s
	}
	return "USB"
}

// freeName is name, or name-2, name-3 ... when a drive by that name is
// already mounted. Callers hold d.mu.
func (d *Drives) freeName(name string) string {
	taken := map[string]bool{}
	for _, m := range d.mounts {
		taken[filepath.Base(m.dir)] = true
	}
	n := name
	for i := 2; taken[n]; i++ {
		n = fmt.Sprintf("%s-%d", name, i)
	}
	return n
}

var blkidField = regexp.MustCompile(`([A-Z_]+)="([^"]*)"`)

// blkid reads a device's file system type and label.
func blkid(node string) (fstype, label string) {
	out, err := exec.Command("blkid", node).Output()
	if err != nil {
		return "", ""
	}
	for _, m := range blkidField.FindAllSubmatch(bytes.TrimSpace(out), -1) {
		switch string(m[1]) {
		case "TYPE":
			fstype = string(m[2])
		case "LABEL":
			label = string(m[2])
		}
	}
	return fstype, label
}

// watcher is the kiosk's drive watcher, started by StartDrives.
var watcher *Drives

// Startup tidies what a previous run of the kiosk, stopped mid-session, left
// behind — devices still with usbip-host, a PC/SC daemon still holding the
// readers — and starts watching for drives.
func Startup() {
	ReleaseStale()
	_ = exec.Command("pkill", "-x", "pcscd").Run()
	watcher = NewDrives()
	go watcher.Run()
}

// HoldDisk unmounts a disk's drives and keeps them unmounted: the installer
// is about to write over it.
func HoldDisk(disk string) {
	if watcher != nil {
		watcher.Hold(disk)
	}
}
