// Package gpt adds a partition to a GUID partition table, the one thing the
// kiosk does to one: a USB stick the ISO was written to as it is has the
// ISO's table, which ends with the ISO, and the stick's free space behind it
// is where a live kiosk keeps its updates (see install.AddLiveSlots).
//
// It edits the table in place and nothing else on the disk: the protective
// MBR stays as xorriso wrote it, with the active entry some BIOSes want and
// the boot code the BIOS starts.
package gpt

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"unicode/utf16"
)

// SectorSize is the only logical sector size this package handles, the one
// USB sticks have and the ISO's table is laid out for.
const SectorSize = 512

const (
	headerSize = 92
	entryName  = 72 // bytes of UTF-16LE in an entry's name
)

var (
	// ErrNoTable is a disk without a valid GPT.
	ErrNoTable = errors.New("gpt: no valid GUID partition table")
	// ErrNoRoom is a disk whose free space is smaller than asked for.
	ErrNoRoom = errors.New("gpt: not enough free space on the disk")
	// ErrFull is a table with no unused entry.
	ErrFull = errors.New("gpt: the partition table is full")
)

// BasicData is the partition type GUID for a FAT file system, in its on-disk
// byte order.
var BasicData = guid("EBD0A0A2-B9E5-4433-87C0-68B6B72699C7")

// Disk is what a table is read from and written to: a block device, or an
// image file in tests.
type Disk interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}

// Partition is one the table has, in sectors.
type Partition struct {
	Num        int // its number, 1 for the first entry
	Start, End int64
}

type header struct {
	raw        []byte
	alternate  int64
	entriesLBA int64
	count      int
	size       int
}

func readHeader(d Disk, lba int64) (*header, error) {
	b := make([]byte, SectorSize)
	if _, err := d.ReadAt(b, lba*SectorSize); err != nil {
		return nil, err
	}
	hs := binary.LittleEndian.Uint32(b[12:])
	if string(b[:8]) != "EFI PART" || hs < headerSize || hs > SectorSize ||
		int64(binary.LittleEndian.Uint64(b[24:])) != lba {
		return nil, ErrNoTable
	}
	raw := b[:hs]
	sum := binary.LittleEndian.Uint32(raw[16:])
	binary.LittleEndian.PutUint32(raw[16:], 0)
	if crc32.ChecksumIEEE(raw) != sum {
		return nil, ErrNoTable
	}
	h := &header{
		raw:        raw,
		alternate:  int64(binary.LittleEndian.Uint64(raw[32:])),
		entriesLBA: int64(binary.LittleEndian.Uint64(raw[72:])),
		count:      int(binary.LittleEndian.Uint32(raw[80:])),
		size:       int(binary.LittleEndian.Uint32(raw[84:])),
	}
	if h.size < 128 || h.size%8 != 0 || h.count < 1 || h.count*h.size > 1<<20 {
		return nil, ErrNoTable
	}
	return h, nil
}

// encode is the header as it is written at lba, with the other copy at alt
// and its entries, whose checksum is sum, at entries.
func (h *header) encode(lba, alt, lastUsable, entries int64, sum uint32) []byte {
	b := make([]byte, SectorSize)
	copy(b, h.raw)
	binary.LittleEndian.PutUint64(b[24:], uint64(lba))
	binary.LittleEndian.PutUint64(b[32:], uint64(alt))
	binary.LittleEndian.PutUint64(b[48:], uint64(lastUsable))
	binary.LittleEndian.PutUint64(b[72:], uint64(entries))
	binary.LittleEndian.PutUint32(b[88:], sum)
	binary.LittleEndian.PutUint32(b[16:], 0)
	binary.LittleEndian.PutUint32(b[16:], crc32.ChecksumIEEE(b[:len(h.raw)]))
	return b
}

func (h *header) entrySectors() int64 {
	return (int64(h.count*h.size) + SectorSize - 1) / SectorSize
}

// Add gives the disk, sectors long, a partition named name in the free space
// after everything the table and its backup cover, starting on a MiB: as much
// of it as there is up to most bytes, and no partition at all when that is
// less than least. A partition of that name already there is returned as it
// is, so an Add that was cut short can be run again.
//
// The backup table moves to the end of the disk, where it belongs; the ISO's
// is at the end of the ISO. It is written first and the primary last, each
// with its entries before its header, so that a power cut at any point leaves
// one of them valid and a disk that boots as before.
func Add(d Disk, sectors int64, name string, typ [16]byte, least, most int64) (Partition, error) {
	h, err := readHeader(d, 1)
	if err != nil {
		return Partition{}, err
	}
	entries := make([]byte, h.count*h.size)
	if _, err := d.ReadAt(entries, h.entriesLBA*SectorSize); err != nil {
		return Partition{}, err
	}
	if crc32.ChecksumIEEE(entries) != binary.LittleEndian.Uint32(h.raw[88:]) {
		return Partition{}, ErrNoTable
	}

	free, used := -1, h.alternate
	for i := 0; i < h.count; i++ {
		e := entries[i*h.size : (i+1)*h.size]
		if bytes.Equal(e[:16], make([]byte, 16)) {
			if free < 0 {
				free = i
			}
			continue
		}
		start, end := int64(binary.LittleEndian.Uint64(e[32:])), int64(binary.LittleEndian.Uint64(e[40:]))
		if decodeName(e[56:56+entryName]) == name {
			return Partition{Num: i + 1, Start: start, End: end}, nil
		}
		used = max(used, end)
	}
	if free < 0 {
		return Partition{}, ErrFull
	}

	alt := sectors - 1
	backup := alt - h.entrySectors()
	lastUsable := backup - 1
	const mib = 1 << 20 / SectorSize
	start := (used + 1 + mib - 1) / mib * mib
	end := min(lastUsable, start+most/SectorSize-1)
	if end < start || (end-start+1)*SectorSize < least {
		return Partition{}, fmt.Errorf("%w: %d MiB free", ErrNoRoom, max(0, lastUsable-start+1)*SectorSize>>20)
	}

	e := entries[free*h.size : (free+1)*h.size]
	clear(e)
	copy(e[:16], typ[:])
	if _, err := rand.Read(e[16:32]); err != nil {
		return Partition{}, err
	}
	e[16+7] = e[16+7]&0x0f | 0x40 // a version 4 GUID, in GPT's byte order
	e[16+8] = e[16+8]&0x3f | 0x80
	binary.LittleEndian.PutUint64(e[32:], uint64(start))
	binary.LittleEndian.PutUint64(e[40:], uint64(end))
	encodeName(e[56:56+entryName], name)
	sum := crc32.ChecksumIEEE(entries)

	oldAlt := h.alternate
	for _, w := range []struct {
		b   []byte
		lba int64
	}{
		{entries, backup},
		{h.encode(alt, 1, lastUsable, backup, sum), alt},
		{entries, h.entriesLBA},
		{h.encode(1, alt, lastUsable, h.entriesLBA, sum), 1},
	} {
		if _, err := d.WriteAt(w.b, w.lba*SectorSize); err != nil {
			return Partition{}, err
		}
		if err := d.Sync(); err != nil {
			return Partition{}, err
		}
	}
	// The old backup header, which nothing points at now, would have tools
	// see a second table. Its entries are left: without a header they are
	// not one.
	if oldAlt != alt && oldAlt > 1 && oldAlt < sectors {
		if _, err := d.WriteAt(make([]byte, SectorSize), oldAlt*SectorSize); err != nil {
			return Partition{}, err
		}
		if err := d.Sync(); err != nil {
			return Partition{}, err
		}
	}
	return Partition{Num: free + 1, Start: start, End: end}, nil
}

// guid turns a GUID's text into its on-disk bytes: the first three fields
// little-endian, the rest as written.
func guid(s string) [16]byte {
	var g [16]byte
	if _, err := hex.Decode(g[:], []byte(strings.ReplaceAll(s, "-", ""))); err != nil {
		panic(err)
	}
	g[0], g[1], g[2], g[3] = g[3], g[2], g[1], g[0]
	g[4], g[5] = g[5], g[4]
	g[6], g[7] = g[7], g[6]
	return g
}

func encodeName(b []byte, s string) {
	for i, c := range utf16.Encode([]rune(s)) {
		if 2*i+1 >= len(b) {
			break
		}
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
}

func decodeName(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}
