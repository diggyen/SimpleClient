package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/gpt"
)

// The partition a USB stick the ISO was written to as it is keeps a live
// kiosk's updates on: as much of the stick's free space as there is, up to
// slotsMost, and no less than slotsLeast, room for two releases.
const (
	slotsLeast = 1 << 30
	slotsMost  = 2 << 30
)

// AddLiveSlots makes the partition a live kiosk keeps its updates on, on the
// USB stick the ISO was written to as it is (dd, Rufus's DD mode), and
// returns its device and its volume ID: behind the ISO, in the stick's free
// space, FAT32 and labelled bootenv.SlotsLabel. The ISO's own partitions,
// and what the stick boots from, stay as they were. A stick Rufus wrote in
// ISO mode needs none; its one partition has the room.
func AddLiveSlots() (dev, uuid string, err error) {
	efi, err := bootenv.FindLabel("SC_EFI")
	if err != nil {
		return "", "", err
	}
	disk := diskOf(filepath.Base(efi))
	if disk == "" || !isISO(filepath.Join(devDir, disk)) {
		return "", "", fmt.Errorf("install: %s is not on a stick the ISO was written to", efi)
	}
	if s := readTrim(filepath.Join(sysBlock, disk, "queue", "logical_block_size")); s != strconv.Itoa(gpt.SectorSize) {
		return "", "", fmt.Errorf("install: %s has %s-byte sectors", disk, s)
	}
	sectors, err := strconv.ParseInt(readTrim(filepath.Join(sysBlock, disk, "size")), 10, 64)
	if err != nil {
		return "", "", err
	}

	f, err := os.OpenFile(filepath.Join(devDir, disk), os.O_RDWR, 0)
	if err != nil {
		return "", "", err
	}
	p, err := gpt.Add(f, sectors, bootenv.SlotsLabel, gpt.BasicData, slotsLeast, slotsMost)
	if err == nil {
		// The kernel reads a disk's table when the disk appears; told of
		// this one partition, it does not need the others let go of.
		err = addPartition(f, p)
	}
	f.Close()
	if err != nil {
		return "", "", err
	}

	dev = filepath.Join(devDir, partName(disk, p.Num))
	for i := 0; i < 50; i++ {
		if _, err = os.Stat(dev); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return "", "", err
	}
	if out, err := exec.Command("mkfs.fat", "-F", "32", "-n", bootenv.SlotsLabel, dev).CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("install: mkfs.fat %s: %v: %s", dev, err, strings.TrimSpace(string(out)))
	}
	out, _ := exec.Command("blkid", dev).Output()
	m := uuidField.FindSubmatch(out)
	if m == nil {
		return "", "", errors.New("install: the new partition has no volume ID")
	}
	return dev, string(m[1]), nil
}

var uuidField = regexp.MustCompile(`(?:^|\s)UUID="([^"]*)"`)

// diskOf names the disk partition part is on, "sdb" for "sdb2".
func diskOf(part string) string {
	disks, _ := os.ReadDir(sysBlock)
	for _, d := range disks {
		if _, err := os.Stat(filepath.Join(sysBlock, d.Name(), part)); err == nil {
			return d.Name()
		}
	}
	return ""
}

// partName names partition n of disk: sdb4, nvme0n1p4, mmcblk0p4.
func partName(disk string, n int) string {
	if c := disk[len(disk)-1]; c >= '0' && c <= '9' {
		return disk + "p" + strconv.Itoa(n)
	}
	return disk + strconv.Itoa(n)
}
