// Package audio plays and records PCM on the kiosk's sound hardware.
//
// It drives ALSA through the aplay/arecord tools shipped in the initramfs
// rather than through the kernel's PCM ioctls directly: the "plughw" layer they
// sit on converts sample rate, channel count and format to whatever the
// hardware accepts, which is the difference between "works on every codec" and
// "works on the one it was tested on".
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Format is a WAVEFORMATEX as RDP's sound channels exchange it.
type Format struct {
	Tag            uint16 // 1 = PCM
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Extra          []byte
}

// FormatPCM is the WAVE_FORMAT_PCM tag, the only encoding played here.
const FormatPCM = 1

// Playable reports whether the format is one this package can play or record:
// uncompressed PCM, 8 or 16 bits, mono or stereo, a sane rate.
func (f Format) Playable() bool {
	return f.Tag == FormatPCM &&
		(f.BitsPerSample == 16 || f.BitsPerSample == 8) &&
		(f.Channels == 1 || f.Channels == 2) &&
		f.SamplesPerSec >= 8000 && f.SamplesPerSec <= 192000
}

// BytesPerSecond is the PCM data rate of the format.
func (f Format) BytesPerSecond() int {
	return int(f.SamplesPerSec) * int(f.Channels) * int(f.BitsPerSample) / 8
}

func (f Format) String() string {
	return fmt.Sprintf("tag=%d %dHz %dch %dbit", f.Tag, f.SamplesPerSec, f.Channels, f.BitsPerSample)
}

// ReadFormat decodes one WAVEFORMATEX.
func ReadFormat(r io.Reader) (Format, error) {
	var f Format
	var hdr struct {
		Tag            uint16
		Channels       uint16
		SamplesPerSec  uint32
		AvgBytesPerSec uint32
		BlockAlign     uint16
		BitsPerSample  uint16
		CbSize         uint16
	}
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return f, err
	}
	f = Format{hdr.Tag, hdr.Channels, hdr.SamplesPerSec, hdr.AvgBytesPerSec, hdr.BlockAlign, hdr.BitsPerSample, nil}
	if hdr.CbSize > 0 {
		f.Extra = make([]byte, hdr.CbSize)
		if _, err := io.ReadFull(r, f.Extra); err != nil {
			return f, err
		}
	}
	return f, nil
}

// WriteFormat encodes one WAVEFORMATEX.
func WriteFormat(w *bytes.Buffer, f Format) {
	_ = binary.Write(w, binary.LittleEndian, struct {
		Tag            uint16
		Channels       uint16
		SamplesPerSec  uint32
		AvgBytesPerSec uint32
		BlockAlign     uint16
		BitsPerSample  uint16
		CbSize         uint16
	}{f.Tag, f.Channels, f.SamplesPerSec, f.AvgBytesPerSec, f.BlockAlign, f.BitsPerSample, uint16(len(f.Extra))})
	w.Write(f.Extra)
}

// alsaFormat is the aplay/arecord -f name for the format's sample type.
func (f Format) alsaFormat() string {
	if f.BitsPerSample == 8 {
		return "U8"
	}
	return "S16_LE"
}
