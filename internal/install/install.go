// Package install puts the kiosk onto a local disk: it lists the disks it can
// go on and runs the installer script (/sbin/sc-install), reporting progress.
package install

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MinSize is the smallest disk offered: the layout needs about 1.7 GB, and a
// disk that small would leave nothing for downloads and logs.
const MinSize = 4 << 30

// Disk is a whole disk the kiosk could be installed on.
type Disk struct {
	Name  string // kernel name, e.g. "nvme0n1"
	Model string
	Size  int64  // bytes
	Bus   string // NVMe, SATA, eMMC, SD, SCSI, Virtio, USB
	// Self is the USB stick the kiosk runs from. It can be installed on
	// too, the way live systems install onto their own stick: the kiosk
	// runs from memory, and the installer copies what it installs into
	// memory before it wipes the stick.
	Self bool
}

// Path is the disk's device node.
func (d Disk) Path() string { return "/dev/" + d.Name }

// Label describes the disk for the screen, e.g. "NVMe  Samsung SSD 980  500 GB".
func (d Disk) Label() string {
	model := d.Model
	if model == "" {
		model = d.Name
	}
	return fmt.Sprintf("%s  %s  %s", d.Bus, model, HumanSize(d.Size))
}

// HumanSize formats a disk size the way disks are sold: decimal units.
func HumanSize(b int64) string {
	switch {
	case b >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(b)/1e12)
	case b >= 1e9:
		return fmt.Sprintf("%d GB", (b+5e8)/1e9)
	default:
		return fmt.Sprintf("%d MB", b/1e6)
	}
}

// sysBlock and devDir are where disks are found; tests point them elsewhere.
var (
	sysBlock = "/sys/block"
	devDir   = "/dev"
)

// busOrder ranks buses for the list: an internal NVMe drive is what a mini PC
// is meant to boot from, a USB disk the least likely target.
var busOrder = map[string]int{"NVMe": 0, "SATA": 1, "eMMC": 2, "SCSI": 3, "Virtio": 4, "SD": 5, "USB": 6}

// Disks lists the disks the kiosk can be installed on, NVMe first: every whole
// disk that is writable and at least MinSize, the stick it runs from included.
func Disks() []Disk {
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		return nil
	}
	var disks []Disk
	for _, e := range entries {
		name := e.Name()
		if skipName(name) {
			continue
		}
		dir := filepath.Join(sysBlock, name)
		if readTrim(filepath.Join(dir, "ro")) == "1" {
			continue
		}
		sectors, _ := strconv.ParseInt(readTrim(filepath.Join(dir, "size")), 10, 64)
		size := sectors * 512
		if size < MinSize {
			continue
		}
		disks = append(disks, Disk{Name: name, Model: model(dir, name), Size: size, Bus: bus(dir, name),
			Self: IsInstallMedium(filepath.Join(devDir, name))})
	}
	sort.SliceStable(disks, func(i, j int) bool {
		if busOrder[disks[i].Bus] != busOrder[disks[j].Bus] {
			return busOrder[disks[i].Bus] < busOrder[disks[j].Bus]
		}
		return disks[i].Name < disks[j].Name
	})
	return disks
}

// skipName rejects block devices that are not disks: loop and RAM devices,
// optical drives, device-mapper and RAID volumes, and an eMMC's boot and RPMB
// areas.
func skipName(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "sr", "fd", "dm-", "md", "nbd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return strings.HasPrefix(name, "mmcblk") && (strings.Contains(name, "boot") || strings.Contains(name, "rpmb"))
}

func bus(dir, name string) string {
	switch {
	case strings.HasPrefix(name, "nvme"):
		return "NVMe"
	case strings.HasPrefix(name, "mmcblk"):
		if readTrim(filepath.Join(dir, "device", "type")) == "SD" {
			return "SD"
		}
		return "eMMC"
	case strings.HasPrefix(name, "vd"):
		return "Virtio"
	}
	// sdX is anything behind the SCSI layer; where it really sits shows in
	// its path through sysfs.
	path, _ := filepath.EvalSymlinks(dir)
	switch {
	case strings.Contains(path, "/usb"):
		return "USB"
	case strings.Contains(path, "/ata"):
		return "SATA"
	}
	return "SCSI"
}

func model(dir, name string) string {
	if strings.HasPrefix(name, "mmcblk") {
		return readTrim(filepath.Join(dir, "device", "name"))
	}
	m := readTrim(filepath.Join(dir, "device", "model"))
	if v := readTrim(filepath.Join(dir, "device", "vendor")); v != "" && v != "ATA" && !strings.HasPrefix(m, v) {
		m = v + " " + m
	}
	return strings.Join(strings.Fields(m), " ")
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// IsInstallMedium reports whether dev holds the SimpleClient ISO — the USB
// stick written with it, booted from right now. Its ISO 9660 primary volume
// descriptor is sector 16: "CD001" at byte 1, the volume ID at byte 40.
func IsInstallMedium(dev string) bool {
	f, err := os.Open(dev)
	if err != nil {
		return false
	}
	defer f.Close()
	pvd := make([]byte, 72)
	if _, err := f.ReadAt(pvd, 16*2048); err != nil {
		return false
	}
	return string(pvd[1:6]) == "CD001" && strings.TrimSpace(string(pvd[40:72])) == "SIMPLECLIENT"
}

// Table is the partition table for this machine: GPT when it booted in UEFI
// mode, MBR when it booted in BIOS mode. Both layouts boot either way; this
// matches what the firmware is set to now.
func Table() string {
	if _, err := os.Stat("/sys/firmware/efi"); err == nil {
		return "gpt"
	}
	return "mbr"
}

// Step names, as the installer script reports them.
const (
	StepMedium     = "medium"
	StepPartition  = "partition"
	StepFormat     = "format"
	StepCopy       = "copy"
	StepBootloader = "bootloader"
	StepFirmware   = "firmware"
)

// Progress receives the installer's steps, numbered from 1 of total.
type Progress func(step, total int, name string)

// Options are what the installed system starts with.
type Options struct {
	Table   string // "gpt" or "mbr"
	Lang    string // UI language, "tr" or "en"
	Kernel  string // the kernel this boot runs, which has just proven itself
	Version string // written into the system slot
}

var installer = "/sbin/sc-install"

// Run installs onto d, wiping it, and reports each step. It returns once the
// installer has finished; the error carries the installer's own explanation.
func Run(d Disk, opt Options, progress Progress) error {
	cmd := exec.Command(installer, d.Path(), opt.Table, opt.Lang)
	cmd.Env = append(os.Environ(), "SC_KERNEL="+opt.Kernel, "SC_VERSION="+opt.Version)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done, failure := follow(out, progress)
	werr := cmd.Wait()
	switch {
	case failure != "":
		return errors.New(failure)
	case werr != nil:
		return werr
	case !done:
		return errors.New("the installer stopped before it finished")
	}
	return nil
}

// follow reads the installer's output: "STEP n total name", "ERROR text",
// "DONE". Anything else is passed through to the log.
func follow(r io.Reader, progress Progress) (done bool, failure string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "STEP "):
			f := strings.Fields(line)
			if len(f) == 4 {
				n, _ := strconv.Atoi(f[1])
				total, _ := strconv.Atoi(f[2])
				if progress != nil {
					progress(n, total, f[3])
				}
			}
		case strings.HasPrefix(line, "ERROR "):
			failure = strings.TrimPrefix(line, "ERROR ")
		case line == "DONE":
			done = true
		default:
			fmt.Fprintln(os.Stderr, "sc-install:", line)
		}
	}
	return done, failure
}
