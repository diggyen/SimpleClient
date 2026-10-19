// Package dvc implements the client side of the dynamic virtual channel
// transport ([MS-RDPEDYC]) — the "drdynvc" static channel that carries the
// dynamic ones, of which the microphone (AUDIO_INPUT) is the one served here.
//
// grdp ships only a stub of this channel, which never answered the server's
// capabilities request, so no dynamic channel ever opened.
package dvc

import (
	"bytes"
	"encoding/binary"
	"log"
	"sync"

	"github.com/tomatome/grdp/core"
	"github.com/tomatome/grdp/plugin"
)

// ChannelName is the static channel carrying the dynamic ones.
const ChannelName = "drdynvc"

const (
	cmdCreate    = 0x01
	cmdDataFirst = 0x02
	cmdData      = 0x03
	cmdClose     = 0x04
	cmdCaps      = 0x05
)

// maxPDU is the largest DVC PDU either side may send.
const maxPDU = 1600

// statusNotFound refuses a channel this client does not implement.
const statusNotFound = 0xC0000225

// Listener serves one named dynamic channel.
type Listener interface {
	// Open is called when the server creates the channel; send writes one
	// message back to the server on it.
	Open(send func([]byte))
	// Data is one complete message from the server.
	Data([]byte)
	// Close is called when the server closes the channel or the session ends.
	Close()
}

type channel struct {
	name     string
	listener Listener
	// Reassembly of a message sent as DATA_FIRST + DATA fragments.
	buf   []byte
	total int
}

// Transport is the drdynvc channel client.
type Transport struct {
	sender    core.ChannelSender
	listeners map[string]func() Listener

	mu   sync.Mutex
	open map[uint32]*channel
}

// New returns a transport serving the given dynamic channels; each factory is
// called once per channel the server opens.
func New(listeners map[string]func() Listener) *Transport {
	return &Transport{listeners: listeners, open: map[uint32]*channel{}}
}

// GetType implements plugin.ChannelTransport.
func (t *Transport) GetType() (string, uint32) {
	return ChannelName, plugin.CHANNEL_OPTION_INITIALIZED | plugin.CHANNEL_OPTION_ENCRYPT_RDP
}

// Sender implements plugin.ChannelTransport.
func (t *Transport) Sender(s core.ChannelSender) { t.sender = s }

// CloseAll ends every open dynamic channel; the session is over.
func (t *Transport) CloseAll() {
	t.mu.Lock()
	open := t.open
	t.open = map[uint32]*channel{}
	t.mu.Unlock()
	for _, ch := range open {
		ch.listener.Close()
	}
}

// Process implements plugin.ChannelTransport.
func (t *Transport) Process(s []byte) {
	if len(s) < 1 {
		return
	}
	hdr := s[0]
	cmd := hdr >> 4
	sp := (hdr >> 2) & 0x3
	cbID := hdr & 0x3
	r := bytes.NewReader(s[1:])

	switch cmd {
	case cmdCaps:
		// Answer with version 2: no compression, which version 3 adds and
		// this client does not implement.
		t.write([]byte{cmdCaps << 4, 0x00, 0x02, 0x00})

	case cmdCreate:
		id, ok := readVar(r, cbID)
		if !ok {
			return
		}
		name := readCString(r)
		factory := t.listeners[name]
		status := uint32(0)
		if factory == nil {
			status = statusNotFound
		}
		resp := &bytes.Buffer{}
		resp.WriteByte(cmdCreate<<4 | idSize(id))
		writeVar(resp, id)
		_ = binary.Write(resp, binary.LittleEndian, status)
		t.write(resp.Bytes())
		if factory == nil {
			log.Printf("dvc: refused channel %q", name)
			return
		}
		ch := &channel{name: name, listener: factory()}
		t.mu.Lock()
		t.open[id] = ch
		t.mu.Unlock()
		log.Printf("dvc: opened channel %q (%d)", name, id)
		ch.listener.Open(func(b []byte) { t.sendData(id, b) })

	case cmdDataFirst:
		id, ok := readVar(r, cbID)
		if !ok {
			return
		}
		total, ok := readVar(r, sp)
		if !ok {
			return
		}
		ch := t.lookup(id)
		if ch == nil {
			return
		}
		rest := make([]byte, r.Len())
		_, _ = r.Read(rest)
		ch.buf, ch.total = rest, int(total)
		if len(ch.buf) >= ch.total {
			ch.listener.Data(ch.buf)
			ch.buf = nil
		}

	case cmdData:
		id, ok := readVar(r, cbID)
		if !ok {
			return
		}
		ch := t.lookup(id)
		if ch == nil {
			return
		}
		rest := make([]byte, r.Len())
		_, _ = r.Read(rest)
		if ch.buf != nil {
			ch.buf = append(ch.buf, rest...)
			if len(ch.buf) >= ch.total {
				ch.listener.Data(ch.buf)
				ch.buf = nil
			}
			return
		}
		ch.listener.Data(rest)

	case cmdClose:
		id, ok := readVar(r, cbID)
		if !ok {
			return
		}
		t.mu.Lock()
		ch := t.open[id]
		delete(t.open, id)
		t.mu.Unlock()
		if ch != nil {
			ch.listener.Close()
			resp := &bytes.Buffer{}
			resp.WriteByte(cmdClose<<4 | idSize(id))
			writeVar(resp, id)
			t.write(resp.Bytes())
		}
	}
}

func (t *Transport) lookup(id uint32) *channel {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.open[id]
}

func (t *Transport) write(b []byte) {
	if t.sender != nil {
		_, _ = t.sender.SendToChannel(ChannelName, b)
	}
}

// sendData sends one message on a dynamic channel, split into DATA_FIRST and
// DATA fragments when it does not fit one PDU.
func (t *Transport) sendData(id uint32, data []byte) {
	idLen := 1 << idSize(id)
	if 1+idLen+len(data) <= maxPDU {
		b := &bytes.Buffer{}
		b.WriteByte(cmdData<<4 | idSize(id))
		writeVar(b, id)
		b.Write(data)
		t.write(b.Bytes())
		return
	}
	total := uint32(len(data))
	first := true
	for len(data) > 0 {
		b := &bytes.Buffer{}
		if first {
			b.WriteByte(cmdDataFirst<<4 | idSize(total)<<2 | idSize(id))
			writeVar(b, id)
			writeVar(b, total)
		} else {
			b.WriteByte(cmdData<<4 | idSize(id))
			writeVar(b, id)
		}
		n := maxPDU - b.Len()
		if n > len(data) {
			n = len(data)
		}
		b.Write(data[:n])
		data = data[n:]
		first = false
		t.write(b.Bytes())
	}
}

// idSize is the 2-bit size code for a value: 0 = 1 byte, 1 = 2, 2 = 4.
func idSize(v uint32) byte {
	switch {
	case v <= 0xFF:
		return 0
	case v <= 0xFFFF:
		return 1
	}
	return 2
}

func writeVar(b *bytes.Buffer, v uint32) {
	switch idSize(v) {
	case 0:
		b.WriteByte(byte(v))
	case 1:
		_ = binary.Write(b, binary.LittleEndian, uint16(v))
	default:
		_ = binary.Write(b, binary.LittleEndian, v)
	}
}

func readVar(r *bytes.Reader, size byte) (uint32, bool) {
	switch size {
	case 0:
		v, err := r.ReadByte()
		return uint32(v), err == nil
	case 1:
		var v uint16
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint32(v), err == nil
	default:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err == nil
	}
}

func readCString(r *bytes.Reader) string {
	var b []byte
	for {
		c, err := r.ReadByte()
		if err != nil || c == 0 {
			return string(b)
		}
		b = append(b, c)
	}
}
