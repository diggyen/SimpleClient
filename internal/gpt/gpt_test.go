package gpt

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// isoDisk is a disk the ISO was written to: the table xorriso makes, its
// backup at the end of the ISO, iso sectors in, on a disk of sectors.
func isoDisk(t *testing.T, iso, sectors int64) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := f.Truncate(sectors * SectorSize); err != nil {
		t.Fatal(err)
	}
	const count, size = 248, 128
	entries := make([]byte, count*size)
	for i, p := range [][2]int64{{64, 279}, {280, 16663}, {16664, iso - 65}} {
		e := entries[i*size:]
		copy(e, BasicData[:])
		e[16] = byte(i + 1)
		binary.LittleEndian.PutUint64(e[32:], uint64(p[0]))
		binary.LittleEndian.PutUint64(e[40:], uint64(p[1]))
		encodeName(e[56:56+entryName], []string{"Gap0", "EFI boot partition", "Gap1"}[i])
	}
	h := &header{raw: make([]byte, headerSize)}
	copy(h.raw, "EFI PART")
	binary.LittleEndian.PutUint32(h.raw[8:], 0x10000)
	binary.LittleEndian.PutUint32(h.raw[12:], headerSize)
	binary.LittleEndian.PutUint64(h.raw[40:], 64)
	binary.LittleEndian.PutUint32(h.raw[80:], count)
	binary.LittleEndian.PutUint32(h.raw[84:], size)
	sum := crc32.ChecksumIEEE(entries)
	alt := iso - 1
	for _, w := range []struct {
		b   []byte
		lba int64
	}{
		{entries, 2},
		{h.encode(1, alt, iso-64, 2, sum), 1},
		{entries, alt - 62},
		{h.encode(alt, 1, iso-64, alt-62, sum), alt},
	} {
		if _, err := f.WriteAt(w.b, w.lba*SectorSize); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// table reads the table back the way firmware would, at lba, and its entries.
func table(t *testing.T, d Disk, lba int64) (*header, []byte) {
	t.Helper()
	h, err := readHeader(d, lba)
	if err != nil {
		t.Fatalf("header at %d: %v", lba, err)
	}
	entries := make([]byte, h.count*h.size)
	if _, err := d.ReadAt(entries, h.entriesLBA*SectorSize); err != nil {
		t.Fatal(err)
	}
	if crc32.ChecksumIEEE(entries) != binary.LittleEndian.Uint32(h.raw[88:]) {
		t.Fatalf("entries of the header at %d do not match their checksum", lba)
	}
	return h, entries
}

func TestAddAfterTheISO(t *testing.T) {
	const iso, sectors = 542240, 8 << 21 // a 278 MB ISO on an 8 GiB stick
	d := isoDisk(t, iso, sectors)
	p, err := Add(d, sectors, "SC_SLOTS", BasicData, 1<<30, 2<<30)
	if err != nil {
		t.Fatal(err)
	}
	if p.Num != 4 || p.Start != 542720 || p.End-p.Start+1 != 2<<30/SectorSize {
		t.Fatalf("got %+v, want partition 4 of 2 GiB on the first MiB after the ISO", p)
	}

	primary, entries := table(t, d, 1)
	backup, backupEntries := table(t, d, sectors-1)
	if primary.alternate != sectors-1 || backup.alternate != 1 || string(entries) != string(backupEntries) {
		t.Fatal("the two tables do not point at each other, or differ")
	}
	if last := int64(binary.LittleEndian.Uint64(primary.raw[48:])); last != sectors-64 {
		t.Fatalf("last usable sector %d, want %d", last, sectors-64)
	}
	if got := decodeName(entries[3*128+56 : 3*128+56+entryName]); got != "SC_SLOTS" {
		t.Fatalf("partition 4 is named %q", got)
	}
	// The ISO's partitions are as they were.
	if got := decodeName(entries[128+56 : 128+56+entryName]); got != "EFI boot partition" {
		t.Fatalf("partition 2 is named %q", got)
	}
	if _, err := readHeader(d, iso-1); !errors.Is(err, ErrNoTable) {
		t.Fatal("the ISO's backup header is still there")
	}

	// Again, as after a power cut that came before the file system did.
	again, err := Add(d, sectors, "SC_SLOTS", BasicData, 1<<30, 2<<30)
	if err != nil || again != p {
		t.Fatalf("again: got %+v, %v; want %+v", again, err, p)
	}
}

// A stick with less room than that is left as it is.
func TestAddWithoutRoom(t *testing.T) {
	const iso, sectors = 542240, 2 << 20 // 1 GiB
	d := isoDisk(t, iso, sectors)
	if _, err := Add(d, sectors, "SC_SLOTS", BasicData, 1<<30, 2<<30); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("got %v, want ErrNoRoom", err)
	}
	if h, _ := table(t, d, 1); h.alternate != iso-1 {
		t.Fatal("the table was changed")
	}
	// The stick's whole free space, when that is enough.
	p, err := Add(d, sectors, "SC_SLOTS", BasicData, 600<<20, 2<<30)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(sectors - 64); p.End != want {
		t.Fatalf("ends at %d, want %d", p.End, want)
	}
}

func TestAddNeedsATable(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(f, 1<<21, "SC_SLOTS", BasicData, 1<<20, 1<<30); !errors.Is(err, ErrNoTable) {
		t.Fatalf("got %v, want ErrNoTable", err)
	}
}

func TestGUID(t *testing.T) {
	// EBD0A0A2-B9E5-4433-87C0-68B6B72699C7 as it is on disk.
	want := [16]byte{0xa2, 0xa0, 0xd0, 0xeb, 0xe5, 0xb9, 0x33, 0x44, 0x87, 0xc0, 0x68, 0xb6, 0xb7, 0x26, 0x99, 0xc7}
	if BasicData != want {
		t.Fatalf("got % x", BasicData)
	}
}
