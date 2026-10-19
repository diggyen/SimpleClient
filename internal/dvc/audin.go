package dvc

import (
	"bytes"
	"encoding/binary"
	"log"

	"github.com/diggyen/SimpleClient/internal/audio"
)

// AudioInputName is the dynamic channel carrying the microphone ([MS-RDPEAI]).
const AudioInputName = "AUDIO_INPUT"

const (
	msgVersion       = 0x01
	msgFormats       = 0x02
	msgOpen          = 0x03
	msgOpenReply     = 0x04
	msgDataIncoming  = 0x05
	msgData          = 0x06
	msgFormatChange  = 0x07
	audinVersion     = 0x00000001
	defaultFrames    = 1024 // frames per packet when the server asks for none
	maxPacketSeconds = 1
)

// Recorder captures microphone audio.
type Recorder interface {
	// Start records f and calls deliver with each packet of packetBytes.
	Start(f audio.Format, packetBytes int, deliver func([]byte)) error
	Stop()
}

// AudioInput serves the AUDIO_INPUT channel: the server asks for the
// microphone, the kiosk streams PCM packets until told to stop.
type AudioInput struct {
	rec     Recorder
	send    func([]byte)
	formats []audio.Format
	frames  int
}

// NewAudioInput returns a listener factory recording through rec.
func NewAudioInput(rec Recorder) func() Listener {
	return func() Listener { return &AudioInput{rec: rec} }
}

// Open implements Listener.
func (a *AudioInput) Open(send func([]byte)) { a.send = send }

// Close implements Listener.
func (a *AudioInput) Close() { a.rec.Stop() }

// Data implements Listener.
func (a *AudioInput) Data(b []byte) {
	if len(b) == 0 {
		return
	}
	r := bytes.NewReader(b[1:])
	switch b[0] {
	case msgVersion:
		out := &bytes.Buffer{}
		out.WriteByte(msgVersion)
		_ = binary.Write(out, binary.LittleEndian, uint32(audinVersion))
		a.send(out.Bytes())

	case msgFormats:
		var n, size uint32
		_ = binary.Read(r, binary.LittleEndian, &n)
		_ = binary.Read(r, binary.LittleEndian, &size)
		a.formats = a.formats[:0]
		for i := uint32(0); i < n; i++ {
			f, err := audio.ReadFormat(r)
			if err != nil {
				break
			}
			if f.Playable() {
				a.formats = append(a.formats, f)
			}
		}
		body := &bytes.Buffer{}
		for _, f := range a.formats {
			audio.WriteFormat(body, f)
		}
		out := &bytes.Buffer{}
		out.WriteByte(msgFormats)
		_ = binary.Write(out, binary.LittleEndian, uint32(len(a.formats)))
		_ = binary.Write(out, binary.LittleEndian, uint32(9+body.Len()))
		out.Write(body.Bytes())
		a.send(out.Bytes())

	case msgOpen:
		var hdr struct {
			FramesPerPacket uint32
			InitialFormat   uint32
		}
		if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
			return
		}
		a.frames = int(hdr.FramesPerPacket)
		a.startFormat(hdr.InitialFormat, true)

	case msgFormatChange:
		var idx uint32
		if err := binary.Read(r, binary.LittleEndian, &idx); err != nil {
			return
		}
		a.startFormat(idx, false)
	}
}

// startFormat confirms the format the server picked and starts recording in
// it; on the initial open it also sends the open reply.
func (a *AudioInput) startFormat(idx uint32, open bool) {
	change := &bytes.Buffer{}
	change.WriteByte(msgFormatChange)
	_ = binary.Write(change, binary.LittleEndian, idx)
	a.send(change.Bytes())

	result := uint32(0)
	var err error
	if int(idx) >= len(a.formats) {
		result = 0x80004005 // E_FAIL
	} else {
		f := a.formats[idx]
		frames := a.frames
		if frames <= 0 || frames > int(f.SamplesPerSec)*maxPacketSeconds {
			frames = defaultFrames
		}
		packet := frames * int(f.Channels) * int(f.BitsPerSample) / 8
		err = a.rec.Start(f, packet, a.deliver)
		if err != nil {
			log.Printf("audin: %v", err)
			result = 0x80004005
		} else {
			log.Printf("audin: recording %s, %d frames per packet", f, frames)
		}
	}
	if open {
		reply := &bytes.Buffer{}
		reply.WriteByte(msgOpenReply)
		_ = binary.Write(reply, binary.LittleEndian, result)
		a.send(reply.Bytes())
	}
}

// deliver sends one captured packet: a Data Incoming notice, then the data.
func (a *AudioInput) deliver(pcm []byte) {
	a.send([]byte{msgDataIncoming})
	a.send(append([]byte{msgData}, pcm...))
}
