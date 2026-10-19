// Package rdpsnd implements the client side of the RDP audio output static
// virtual channel ([MS-RDPEA]): the server streams PCM blocks, the kiosk plays
// them and confirms each one, which is what paces the server.
//
// Only uncompressed PCM is offered. On the LAN this kiosk runs on, 1.4 Mbit/s
// of CD-quality stereo is nothing next to the screen updates, and decoding no
// codec keeps the CPU free for the picture.
package rdpsnd

import (
	"bytes"
	"encoding/binary"
	"log"
	"sync"
	"time"

	"github.com/diggyen/SimpleClient/internal/audio"
	"github.com/tomatome/grdp/core"
	"github.com/tomatome/grdp/plugin"
)

// ChannelName is the static virtual channel's name.
const ChannelName = "rdpsnd"

// PDU types.
const (
	sndcClose       = 0x01
	sndcWave        = 0x02
	sndcSetVolume   = 0x03
	sndcWaveConfirm = 0x05
	sndcTraining    = 0x06
	sndcFormats     = 0x07
	sndcQualityMode = 0x0C
	sndcWave2       = 0x0D
)

const (
	tssndcapsAlive = 0x00000001
	// clientVersion 6 asks for the WaveInfo + Wave pair rather than Wave2,
	// which every server version supports; Wave2 is handled too.
	clientVersion = 6
	highQuality   = 0x0002
)

// Player is where decoded audio goes.
type Player interface {
	// Play writes one block, blocking while the device is full.
	Play(f audio.Format, pcm []byte) error
	// Stop releases the device.
	Stop()
}

type block struct {
	format    audio.Format
	pcm       []byte
	timestamp uint16
	blockNo   uint8
	received  time.Time
}

// Channel is the rdpsnd channel client.
type Channel struct {
	player Player
	sender core.ChannelSender

	formats []audio.Format // the list sent to the server; indexes refer to it

	// A WaveInfo PDU is followed by a bare Wave PDU carrying the rest of the
	// samples; pending holds the first until the second arrives.
	pending *block
	waveLen int

	// Blocks are played on their own goroutine. Writing to the sound device
	// blocks for as long as the sound lasts, and doing that on the protocol
	// reader would hold up every screen update behind it.
	queue chan block
	once  sync.Once
	start sync.Once
}

// New returns a channel that plays through p.
func New(p Player) *Channel {
	return &Channel{player: p, queue: make(chan block, 64)}
}

// GetType implements plugin.ChannelTransport.
func (c *Channel) GetType() (string, uint32) {
	return ChannelName, plugin.CHANNEL_OPTION_INITIALIZED | plugin.CHANNEL_OPTION_ENCRYPT_RDP |
		plugin.CHANNEL_OPTION_COMPRESS_RDP | plugin.CHANNEL_OPTION_SHOW_PROTOCOL
}

// Sender implements plugin.ChannelTransport.
func (c *Channel) Sender(s core.ChannelSender) { c.sender = s }

// Close stops playback; the session is over.
func (c *Channel) Close() {
	c.once.Do(func() { close(c.queue) })
}

// Process implements plugin.ChannelTransport: one complete PDU from the server.
func (c *Channel) Process(s []byte) {
	if c.pending != nil {
		c.finishWave(s)
		return
	}
	if len(s) < 4 {
		return
	}
	msgType := s[0]
	body := s[4:]
	switch msgType {
	case sndcFormats:
		c.onFormats(body)
	case sndcTraining:
		c.onTraining(body)
	case sndcWave:
		c.onWaveInfo(s)
	case sndcWave2:
		c.onWave2(body)
	case sndcClose:
		c.player.Stop()
	case sndcSetVolume:
		// The server scales the samples itself; nothing to do.
	}
}

func (c *Channel) send(msgType byte, body []byte) {
	if c.sender == nil {
		return
	}
	b := &bytes.Buffer{}
	b.WriteByte(msgType)
	b.WriteByte(0)
	_ = binary.Write(b, binary.LittleEndian, uint16(len(body)))
	b.Write(body)
	_, _ = c.sender.SendToChannel(ChannelName, b.Bytes())
}

// onFormats answers the server's format list with the playable subset, in the
// server's order, and starts the playback goroutine.
func (c *Channel) onFormats(body []byte) {
	r := bytes.NewReader(body)
	var hdr struct {
		Flags, Volume, Pitch uint32
		DGramPort            uint16
		NumFormats           uint16
		LastBlockConfirmed   uint8
		Version              uint16
		Pad                  uint8
	}
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return
	}
	c.formats = c.formats[:0]
	for i := 0; i < int(hdr.NumFormats); i++ {
		f, err := audio.ReadFormat(r)
		if err != nil {
			break
		}
		if f.Playable() {
			c.formats = append(c.formats, f)
		}
	}
	log.Printf("rdpsnd: server v%d offers %d formats, %d playable", hdr.Version, hdr.NumFormats, len(c.formats))

	out := &bytes.Buffer{}
	_ = binary.Write(out, binary.LittleEndian, struct {
		Flags, Volume, Pitch uint32
		DGramPort            uint16
		NumFormats           uint16
		LastBlockConfirmed   uint8
		Version              uint16
		Pad                  uint8
	}{tssndcapsAlive, 0xFFFFFFFF, 0, 0, uint16(len(c.formats)), 0, clientVersion, 0})
	for _, f := range c.formats {
		audio.WriteFormat(out, f)
	}
	c.send(sndcFormats, out.Bytes())

	if hdr.Version >= 6 {
		q := &bytes.Buffer{}
		_ = binary.Write(q, binary.LittleEndian, [2]uint16{highQuality, 0})
		c.send(sndcQualityMode, q.Bytes())
	}
	c.start.Do(func() { go c.playLoop() })
}

func (c *Channel) onTraining(body []byte) {
	if len(body) < 4 {
		return
	}
	c.send(sndcTraining, body[:4]) // echo wTimeStamp and wPackSize
}

// onWaveInfo handles the first half of a block. Its last four bytes are the
// first four of the samples; the following Wave PDU carries the rest, behind
// four bytes of padding that those stand in for.
func (c *Channel) onWaveInfo(s []byte) {
	if len(s) < 16 {
		return
	}
	bodySize := int(binary.LittleEndian.Uint16(s[2:4]))
	body := s[4:]
	b := &block{
		timestamp: binary.LittleEndian.Uint16(body[0:2]),
		blockNo:   body[4],
		received:  time.Now(),
	}
	formatNo := int(binary.LittleEndian.Uint16(body[2:4]))
	if formatNo < len(c.formats) {
		b.format = c.formats[formatNo]
	}
	b.pcm = append([]byte(nil), body[8:12]...)
	c.pending = b
	c.waveLen = bodySize - 12 // samples beyond the four carried here
}

func (c *Channel) finishWave(s []byte) {
	b := c.pending
	c.pending = nil
	if len(s) >= 4 {
		rest := s[4:]
		if c.waveLen >= 0 && c.waveLen < len(rest) {
			rest = rest[:c.waveLen]
		}
		b.pcm = append(b.pcm, rest...)
	}
	c.enqueue(*b)
}

func (c *Channel) onWave2(body []byte) {
	if len(body) < 12 {
		return
	}
	b := block{
		timestamp: binary.LittleEndian.Uint16(body[0:2]),
		blockNo:   body[4],
		received:  time.Now(),
		pcm:       append([]byte(nil), body[12:]...),
	}
	formatNo := int(binary.LittleEndian.Uint16(body[2:4]))
	if formatNo < len(c.formats) {
		b.format = c.formats[formatNo]
	}
	c.enqueue(b)
}

// enqueue hands a block to the player. If the player has fallen hopelessly
// behind, the block is dropped but still confirmed: a gap in the sound is
// better than a server that stops sending because it is owed confirmations.
func (c *Channel) enqueue(b block) {
	defer func() { _ = recover() }() // queue closed: session over
	select {
	case c.queue <- b:
	default:
		c.confirm(b)
	}
}

func (c *Channel) playLoop() {
	for b := range c.queue {
		if b.format.Playable() {
			if err := c.player.Play(b.format, b.pcm); err != nil {
				log.Printf("rdpsnd: play: %v", err)
			}
		}
		c.confirm(b)
	}
	c.player.Stop()
}

// confirm tells the server a block has been played. The timestamp is the
// server's plus the time the block spent here, which the server uses to
// measure latency.
func (c *Channel) confirm(b block) {
	ts := b.timestamp + uint16(time.Since(b.received)/time.Millisecond)
	body := &bytes.Buffer{}
	_ = binary.Write(body, binary.LittleEndian, ts)
	body.WriteByte(b.blockNo)
	body.WriteByte(0)
	c.send(sndcWaveConfirm, body.Bytes())
}

