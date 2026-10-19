package usb

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeUSB builds /sys/bus/usb/devices with one device per spec: "busid
// class,class,... [net]", interface classes in hex.
func fakeUSB(t *testing.T, specs ...string) string {
	t.Helper()
	root := t.TempDir()
	sysDevices = root
	for n, s := range specs {
		f := strings.Fields(s)
		dir := filepath.Join(root, f[0])
		write(t, dir, "busnum", strings.SplitN(f[0], "-", 2)[0])
		write(t, dir, "devnum", itoa(n+2))
		write(t, dir, "speed", "480")
		write(t, dir, "idVendor", "04f9")
		write(t, dir, "idProduct", "0042")
		write(t, dir, "bDeviceClass", "00")
		write(t, dir, "bConfigurationValue", "1")
		write(t, dir, "bNumConfigurations", "1")
		write(t, dir, "product", "Device "+f[0])
		for i, c := range strings.Split(f[1], ",") {
			idir := filepath.Join(dir, f[0]+":1."+itoa(i))
			write(t, idir, "bInterfaceClass", c)
			write(t, idir, "bInterfaceSubClass", "00")
			write(t, idir, "bInterfaceProtocol", "00")
			if len(f) > 2 && f[2] == "net" {
				if err := os.MkdirAll(filepath.Join(idir, "net", "eth1"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	// A root hub and an interface entry at the top level, as in sysfs.
	write(t, filepath.Join(root, "usb1"), "busnum", "1")
	write(t, filepath.Join(root, "1-1:1.0"), "bInterfaceClass", "09")
	return root
}

func write(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestKind(t *testing.T) {
	fakeUSB(t,
		"1-1 03,03",      // keyboard with a media-key interface
		"1-2 08",         // flash drive
		"1-3 07,ff,08",   // printer with a card slot
		"1-4 0b",         // smart-card reader
		"1-5 01,01,03",   // headset
		"1-6 ff,ff net",  // USB network adapter
		"2-1.4 e0,e0",    // Bluetooth
		"2-1.5 0e,0e,01", // webcam with a microphone
		"2-2 02,0a",      // serial adapter (CDC ACM)
	)
	want := map[string]Kind{
		"1-1": Local, "1-2": Storage, "1-3": Redirect, "1-4": Redirect, "1-5": Local,
		"1-6": Local, "2-1.4": Local, "2-1.5": Local, "2-2": Redirect,
	}
	devs := Scan()
	if len(devs) != len(want) {
		t.Fatalf("Scan found %d devices, want %d: %v", len(devs), len(want), devs)
	}
	for _, d := range devs {
		if d.Kind() != want[d.BusID] {
			t.Errorf("%s: %s, want %s", d.BusID, d.Kind(), want[d.BusID])
		}
	}
	if !devs[3].SmartCard() || devs[2].SmartCard() {
		t.Errorf("SmartCard: reader %v, printer %v", devs[3].SmartCard(), devs[2].SmartCard())
	}
}

func TestIsBusID(t *testing.T) {
	for name, want := range map[string]bool{
		"1-1": true, "2-1.4.3": true, "usb1": false, "1-1:1.0": false, "1-": false, "x-1": false,
	} {
		if isBusID(name) != want {
			t.Errorf("isBusID(%q) = %v", name, !want)
		}
	}
}

// The device list is what usbip list -r and the server's agent read:
// struct usbip_usb_device, 312 bytes, then four bytes per interface.
func TestDevlistReply(t *testing.T) {
	d := Device{BusID: "1-3", Path: "/sys/devices/pci0000:00/0000:00:14.0/usb1/1-3",
		Bus: 1, Dev: 7, Speed: 3, Vendor: 0x04f9, Product: 0x0042, BCD: 0x0100,
		Config: 1, NumConfigs: 1, Interfaces: []Interface{{7, 1, 2}, {0xff, 0, 0}}}
	b := devlistReply([]Device{d})
	if len(b) != 8+4+312+2*4 {
		t.Fatalf("reply is %d bytes", len(b))
	}
	if v, code := binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:]); v != 0x0111 || code != 0x0005 {
		t.Fatalf("header %04x %04x", v, code)
	}
	if n := binary.BigEndian.Uint32(b[8:]); n != 1 {
		t.Fatalf("%d devices", n)
	}
	rec := b[12:]
	if !strings.HasPrefix(string(rec[:256]), d.Path+"\x00") || !strings.HasPrefix(string(rec[256:288]), "1-3\x00") {
		t.Fatal("path or bus ID wrong")
	}
	if binary.BigEndian.Uint32(rec[288:]) != 1 || binary.BigEndian.Uint32(rec[292:]) != 7 ||
		binary.BigEndian.Uint32(rec[296:]) != 3 || binary.BigEndian.Uint16(rec[300:]) != 0x04f9 ||
		binary.BigEndian.Uint16(rec[302:]) != 0x0042 || rec[311] != 2 {
		t.Fatalf("record % x", rec[288:312])
	}
	if rec[312] != 7 || rec[313] != 1 || rec[314] != 2 || rec[316] != 0xff {
		t.Fatalf("interfaces % x", rec[312:])
	}
}

// Windows gets the devices plugged in now by address, in hex, and hotplug
// for the rest; drives always go by name.
func TestArgs(t *testing.T) {
	fakeUSB(t, "1-2 08", "1-3 07,ff", "1-4 0b", "2-1 ff")
	s := &Session{}
	got := strings.Join(s.Args(), " ")
	want := "/drive:hotplug,* /usb:addr:1:3#2:5,auto,filter:1#3#8#9#b#e0"
	if got != want {
		t.Fatalf("Args() = %q\nwant      %q", got, want)
	}
	s.exp = &Exporter{}
	if got := strings.Join(s.Args(), " "); got != "/drive:hotplug,*" {
		t.Fatalf("with the agent: %q", got)
	}
}

func TestDirName(t *testing.T) {
	for label, want := range map[string]string{
		"KINGSTON": "KINGSTON", "USB DISK": "USB_DISK", "": "USB", "...": "USB",
		"Fotoğraflar 2024": "Fotoğraflar_2024", "a/b\\c:d": "abcd",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789": "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
	} {
		if got := dirName(label); got != want {
			t.Errorf("dirName(%q) = %q, want %q", label, got, want)
		}
	}
}

// fakeBlock builds /sys/block with disks on fake USB devices: "disk busid
// size part,part,...". Partition probes come from labels: "dev=type:label".
func fakeBlock(t *testing.T, usbRoot string, specs ...string) {
	t.Helper()
	root := t.TempDir()
	sysBlock = root
	devDir = t.TempDir()
	mediaDir = t.TempDir()
	for _, s := range specs {
		f := strings.Fields(s)
		// The disk's real place: under its USB device, as sysfs links it.
		dir := filepath.Join(usbRoot, f[1], f[1]+":1.0", "host0", "block", f[0])
		write(t, dir, "size", f[2])
		write(t, dir, "diskseq", "7")
		if len(f) > 3 {
			for _, p := range strings.Split(f[3], ",") {
				write(t, filepath.Join(dir, p), "partition", "1")
			}
		}
		if err := os.Symlink(dir, filepath.Join(root, f[0])); err != nil {
			t.Skip("no symlinks here:", err)
		}
	}
}

type fakeOps struct {
	labels  map[string][2]string // node base -> type, label
	mounted map[string]string    // dir -> node
	fail    map[string]bool      // node base -> mount fails
}

func useFakeOps(t *testing.T, labels map[string][2]string) *fakeOps {
	t.Helper()
	ops := &fakeOps{labels: labels, mounted: map[string]string{}, fail: map[string]bool{}}
	m, u, p, i := mountFS, unmountFS, probeFS, isMedium
	t.Cleanup(func() { mountFS, unmountFS, probeFS, isMedium = m, u, p, i })
	mountFS = func(node, dir, fstype, opts string, ro bool) error {
		if ops.fail[filepath.Base(node)] {
			return os.ErrPermission
		}
		ops.mounted[dir] = node
		return nil
	}
	unmountFS = func(dir string) error { delete(ops.mounted, dir); return nil }
	probeFS = func(node string) (string, string) {
		l := ops.labels[filepath.Base(node)]
		return l[0], l[1]
	}
	isMedium = func(string) bool { return false }
	return ops
}

func (o *fakeOps) dirs() string {
	var d []string
	for dir, node := range o.mounted {
		d = append(d, filepath.Base(dir)+"="+filepath.Base(node))
	}
	return strings.Join(sortStrings(d), " ")
}

func sortStrings(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func TestDrives(t *testing.T) {
	usbRoot := fakeUSB(t, "1-2 08", "1-3 08", "1-4 07,08", "1-5 08", "1-6 08")
	fakeBlock(t, usbRoot,
		"sdb 1-2 1000 sdb1,sdb2", // a stick with two partitions
		"sdc 1-3 1000",           // a stick formatted without a partition table
		"sdd 1-4 1000 sdd1",      // the card slot of a printer
		"sde 1-5 0",              // a card reader with no card in
		"sdf 1-6 1000 sdf1,sdf2", // a stick with SimpleClient installed on it
	)
	ops := useFakeOps(t, map[string][2]string{
		"sdb1": {"vfat", "KINGSTON"}, "sdb2": {"ntfs", "KINGSTON"}, "sdc": {"exfat", ""},
		"sdd1": {"vfat", "CARD"}, "sdf1": {"vfat", "SC_ESP"}, "sdf2": {"ext4", "SC_DATA"},
	})
	d := NewDrives()
	d.Poll()
	if got, want := ops.dirs(), "KINGSTON-2=sdb2 KINGSTON=sdb1 USB=sdc"; got != want && got != "KINGSTON-2=sdb1 KINGSTON=sdb2 USB=sdc" {
		t.Fatalf("mounted %q, want %q", got, want)
	}

	// The stick is pulled out: its drives go.
	os.Remove(filepath.Join(sysBlock, "sdb"))
	d.Poll()
	if got := ops.dirs(); got != "USB=sdc" {
		t.Fatalf("after unplugging: %q", got)
	}

	// The installer is about to write over sdc.
	d.Hold("sdc")
	d.Poll()
	if got := ops.dirs(); got != "" {
		t.Fatalf("after Hold: %q", got)
	}
}

// A drive that will not mount is tried a few times, then left alone until
// its medium changes.
func TestDrives_GivesUp(t *testing.T) {
	usbRoot := fakeUSB(t, "1-2 08")
	fakeBlock(t, usbRoot, "sdb 1-2 1000 sdb1")
	ops := useFakeOps(t, map[string][2]string{"sdb1": {"vfat", "X"}})
	ops.fail["sdb1"] = true
	n := 0
	mount := mountFS
	mountFS = func(node, dir, fstype, opts string, ro bool) error { n++; return mount(node, dir, fstype, opts, ro) }
	d := NewDrives()
	for i := 0; i < 6; i++ {
		d.Poll()
	}
	// Each attempt tries read-write, then read-only.
	if n != 2*mountAttempts {
		t.Fatalf("%d mount calls, want %d", n, 2*mountAttempts)
	}
	write(t, filepath.Join(sysBlock, "sdb"), "diskseq", "8")
	ops.fail["sdb1"] = false
	d.Poll()
	if ops.dirs() != "X=sdb1" {
		t.Fatalf("a new medium was not mounted: %q", ops.dirs())
	}
}
