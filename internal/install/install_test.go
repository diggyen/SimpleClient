package install

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeSys builds a /sys/block and /dev with the given disks: name → size in
// sectors, plus a model file.
func fakeSys(t *testing.T, disks map[string]int64) {
	t.Helper()
	root := t.TempDir()
	sysBlock = filepath.Join(root, "sys", "block")
	devDir = filepath.Join(root, "dev")
	os.MkdirAll(devDir, 0o755)
	for name, sectors := range disks {
		dir := filepath.Join(sysBlock, name)
		os.MkdirAll(filepath.Join(dir, "device"), 0o755)
		os.WriteFile(filepath.Join(dir, "size"), []byte(strconv.FormatInt(sectors, 10)+"\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "ro"), []byte("0\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "device", "model"), []byte("Model "+name+"\n"), 0o644)
		os.WriteFile(filepath.Join(devDir, name), make([]byte, 64*1024), 0o644)
	}
}

func TestDisksOrdersNVMeFirstAndSkipsWhatIsNotADisk(t *testing.T) {
	const gb = 1 << 30 / 512
	fakeSys(t, map[string]int64{
		"sda":          64 * gb,
		"nvme0n1":      256 * gb,
		"loop0":        64 * gb,
		"sr0":          1 * gb,
		"mmcblk0boot0": 8 * gb,
		"sdb":          2 * gb, // too small
	})
	disks := Disks()
	var names []string
	for _, d := range disks {
		names = append(names, d.Name)
	}
	if got := strings.Join(names, ","); got != "nvme0n1,sda" {
		t.Fatalf("got %s", got)
	}
	if disks[0].Bus != "NVMe" || disks[0].Model != "Model nvme0n1" {
		t.Fatalf("got %+v", disks[0])
	}
}

// The stick the kiosk runs from is offered too, marked as itself.
func TestDisksMarksTheInstallMedium(t *testing.T) {
	const gb = 1 << 30 / 512
	fakeSys(t, map[string]int64{"sda": 16 * gb, "sdb": 16 * gb})
	// sdb carries the ISO: an ISO 9660 primary volume descriptor at sector 16.
	pvd := make([]byte, 64*1024)
	copy(pvd[16*2048+1:], "CD001")
	copy(pvd[16*2048+40:], "SIMPLECLIENT                    ")
	os.WriteFile(filepath.Join(devDir, "sdb"), pvd, 0o644)

	disks := Disks()
	if len(disks) != 2 || disks[0].Name != "sda" || disks[0].Self || disks[1].Name != "sdb" || !disks[1].Self {
		t.Fatalf("got %+v", disks)
	}
}

// A stick Rufus wrote in ISO mode is no ISO: one FAT partition of it holds
// the ISO's files, labelled with the ISO's volume ID cut to 11 characters.
func TestDisksMarksAStickRufusWroteInISOMode(t *testing.T) {
	const gb = 1 << 30 / 512
	fakeSys(t, map[string]int64{"sda": 16 * gb, "sdb": 16 * gb})
	for _, p := range []string{"sda1", "sdb1"} {
		os.MkdirAll(filepath.Join(sysBlock, p[:3], p), 0o755)
		os.WriteFile(filepath.Join(sysBlock, p[:3], p, "partition"), []byte("1\n"), 0o644)
	}
	f := fsLabel
	t.Cleanup(func() { fsLabel = f })
	labels := map[string]string{"sda1": "DATA", "sdb1": "SIMPLECLIEN"}
	fsLabel = func(dev string) string { return labels[filepath.Base(dev)] }

	disks := Disks()
	if len(disks) != 2 || disks[0].Self || !disks[1].Self {
		t.Fatalf("got %+v", disks)
	}
}

func TestFsLabelReadsBlkid(t *testing.T) {
	for out, want := range map[string]string{
		`/dev/sdb1: LABEL="SIMPLECLIEN" UUID="1A2B-3C4D" TYPE="vfat"`:              "SIMPLECLIEN",
		`/dev/sdb1: UUID="1A2B-3C4D" TYPE="vfat" PARTLABEL="Microsoft basic data"`: "",
		`/dev/sdb1: PARTLABEL="x" LABEL="SIMPLECLIEN" TYPE="vfat"`:                 "SIMPLECLIEN",
	} {
		got := ""
		if m := labelField.FindStringSubmatch(out); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", out, got, want)
		}
	}
}

func TestFollowReportsStepsAndFailure(t *testing.T) {
	out := "STEP 1 6 medium\nnoise\nSTEP 2 6 partition\nERROR partitioning failed\n"
	var steps []string
	done, failure := follow(strings.NewReader(out), func(n, total int, name string) {
		steps = append(steps, strconv.Itoa(n)+"/"+strconv.Itoa(total)+" "+name)
	})
	if done || failure != "partitioning failed" {
		t.Fatalf("done=%v failure=%q", done, failure)
	}
	if strings.Join(steps, ";") != "1/6 medium;2/6 partition" {
		t.Fatalf("steps %v", steps)
	}
	done, failure = follow(strings.NewReader("STEP 6 6 firmware\nDONE\n"), nil)
	if !done || failure != "" {
		t.Fatalf("done=%v failure=%q", done, failure)
	}
}

func TestHumanSize(t *testing.T) {
	for in, want := range map[int64]string{500107862016: "500 GB", 2000398934016: "2.0 TB", 8 << 30: "9 GB"} {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %s, want %s", in, got, want)
		}
	}
}
