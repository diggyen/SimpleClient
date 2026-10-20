package rdp

import (
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"github.com/diggyen/SimpleClient/internal/audio"
	"github.com/diggyen/SimpleClient/internal/dvc"
	"github.com/diggyen/SimpleClient/internal/rdpsnd"
	"github.com/tomatome/grdp/client"
	"github.com/tomatome/grdp/glog"
	"github.com/tomatome/grdp/plugin"
)

// Credentials holds RDP login credentials.
type Credentials struct {
	Username string
	Password string
	Domain   string
}

// connectTimeout bounds the whole handshake: TCP dial, x224, NLA, MCS,
// licensing and capability exchange.
//
// Thirty seconds rather than ten. The NLA step is CPU-bound public-key work,
// and this runs on thin-client hardware — the sort of machine this image exists
// for, and slower still under emulation. Ten seconds was enough on a developer
// laptop and timed out on anything weaker, reporting "server unreachable" for a
// server that was answering perfectly well. The connecting screen animates
// throughout, so a longer wait reads as progress rather than a hang.
const connectTimeout = 30 * time.Second

// updateBacklog is how many screen updates may be queued before the RDP reader
// is made to wait. Each update is one bitmap PDU's worth of tiles.
//
// RDP never resends a tile it has already sent, so a dropped tile is a hole in
// the desktop that stays there until something happens to repaint that exact
// region. Queue deeply and then apply backpressure rather than discarding:
// a session that briefly lags is far better than one permanently full of black
// rectangles.
const updateBacklog = 128

// ColorDepth is the session colour depth requested from the server: 16 or 24
// bits. 16 is the default because it halves the bitmap stream and decodes
// fastest, which is what keeps a thin client smooth; set from GRUB with bpp=24
// where banded gradients matter more than frame rate.
var ColorDepth = 16

// KeyboardLayout is the Windows keyboard layout id announced to the server for
// the next connection (see LayoutID). The server, xrdp included, picks its
// scancode-to-character mapping from it, so it has to match the physical
// keyboard or every key off the US layout types the wrong character.
var KeyboardLayout uint32 = LayoutUS

// Windows keyboard layout ids (KLIDs).
const (
	LayoutUS  uint32 = 0x00000409
	LayoutTRQ uint32 = 0x0000041F
	LayoutTRF uint32 = 0x0001041F
)

// LayoutID maps the kiosk's layout names onto KLIDs.
func LayoutID(name string) uint32 {
	switch name {
	case "tr-q":
		return LayoutTRQ
	case "tr-f":
		return LayoutTRF
	}
	return LayoutUS
}

// Client wraps grdp/client and provides frame streaming and input forwarding.
type Client struct {
	g      *client.Client
	frames chan []image.Image
	raw    chan []image.Image
	done   chan struct{}
	width  int
	height int

	// closeOnce guards done; frames is closed only by the pump goroutine, which
	// is its sole owner, so no send can ever race with the close.
	closeOnce sync.Once

	// Pointer moves are coalesced: the UI stores the newest position and a
	// single sender goroutine forwards whatever is newest when the socket is
	// free. A 1000 Hz mouse would otherwise put a thousand PDUs a second on the
	// wire and, with the socket momentarily full, stall the input loop.
	mouseXY   atomic.Uint64
	mouseKick chan struct{}

	// send serialises everything written to the server, so a button press can
	// never overtake the move that preceded it.
	send sync.Mutex

	// failed is set when a live session ends on an error — a dropped network,
	// a protocol fault — as opposed to the server closing it cleanly (the user
	// logged out) or Close being called here. Only a failed session is worth
	// reconnecting.
	failed atomic.Bool

	// closeChannels stops sound playback and recording when the session ends.
	closeChannels func()
}

// Audio is the sound hardware the sessions play to and record from; nil
// leaves sound off, as on a machine with no sound card.
var Audio *audio.Sink

// Failed reports whether the session ended on an error rather than a clean
// close from either side.
func (c *Client) Failed() bool { return c.failed.Load() }

// Rejected is always false: the built-in client cannot tell a server that
// turned the password down from one that logged the user off (see
// Engine.Rejected).
func (c *Client) Rejected() bool { return false }

// Debug turns on the RDP library's full handshake logging. It is a diagnostic
// switch only: grdp prints the password in clear text at this level, which is
// why the default stays at WARN.
var Debug bool

// New creates and connects an RDP client.
func New(addr string, creds Credentials, width, height int) (*Client, error) {
	setting := client.NewSetting()
	setting.Width = width
	setting.Height = height
	setting.LogLevel = glog.WARN
	setting.KeyboardLayout = KeyboardLayout
	setting.ColorDepth = ColorDepth

	// Sound out and microphone in. Both are announced only when there is
	// somewhere to play: a server told the client takes audio sends it.
	var snd *rdpsnd.Channel
	var dyn *dvc.Transport
	if Audio != nil {
		snd = rdpsnd.New(Audio)
		dyn = dvc.New(map[string]func() dvc.Listener{
			dvc.AudioInputName: dvc.NewAudioInput(Audio.Mic()),
		})
		setting.Channels = []plugin.ChannelTransport{snd, dyn}
	}
	if Debug {
		setting.LogLevel = glog.DEBUG
	}

	// For domain authentication pass "domain\user" as the user string.
	user := creds.Username
	if creds.Domain != "" {
		user = creds.Domain + "\\" + creds.Username
	}

	g := client.NewClient(addr, user, creds.Password, client.TC_RDP, setting)

	c := newClient(width, height)
	c.g = g
	c.closeChannels = func() {
		if snd != nil {
			snd.Close()
		}
		if dyn != nil {
			dyn.CloseAll()
		}
	}

	// Login has to come first. grdp's RdpClient is an empty struct until Login
	// builds the protocol stack, and every On* registration dereferences the
	// pdu client it creates there — attaching handlers beforehand panics with a
	// nil pointer. Login is not the blocking call its name suggests: it dials,
	// starts the x224 handshake and returns, leaving the session running on
	// grdp's own reader goroutine.
	if err := g.Login(); err != nil {
		c.shutdown()
		return nil, fmt.Errorf("RDP connect %s: %w", addr, err)
	}

	readyCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)

	// "ready", not "success". grdp's On* helpers all subscribe to the pdu layer,
	// and pdu only ever emits close/error/ready/bitmap/orders/color — "success"
	// comes from the sec layer and never reaches a subscriber on an RDP
	// connection. Waiting for it means every connection attempt times out even
	// after the server has logged the user in. "ready" fires on the server font
	// map PDU, which is the point the session is actually live.
	var up atomic.Bool
	g.OnReady(func() {
		up.Store(true)
		select {
		case readyCh <- struct{}{}:
		default:
		}
	})

	g.OnError(func(e error) {
		// An error after the session is up is a dead session: end it, so the
		// UI notices instead of sitting on a frozen desktop.
		if up.Load() {
			glog.Error("rdp: session error:", e)
			c.failed.Store(true)
			c.shutdown()
			return
		}
		select {
		case errCh <- e:
		default:
		}
	})

	g.OnClose(c.shutdown)

	// grdp delivers one bitmap PDU at a time, often dozens of tiles. They are
	// decoded here, on grdp's reader goroutine, and handed over as one update so
	// the writer can paint them together and redraw the pointer once.
	g.OnBitmap(func(bitmaps []client.Bitmap) {
		tiles := make([]image.Image, 0, len(bitmaps))
		for _, bm := range bitmaps {
			tiles = append(tiles, bitmapToImage(bm))
		}
		c.push(tiles)
	})

	go c.mouseSender()

	// Wait for the session to come up, fail, or time out.
	select {
	case <-readyCh:
		return c, nil
	case err := <-errCh:
		g.Close()
		c.shutdown()
		return nil, fmt.Errorf("RDP connect %s: %w", addr, err)
	case <-time.After(connectTimeout):
		g.Close()
		c.shutdown()
		return nil, fmt.Errorf("RDP connect %s: timeout after %s", addr, connectTimeout)
	}
}

// newClient builds the channel plumbing and starts the pump. The protocol
// client is attached by New.
func newClient(width, height int) *Client {
	c := &Client{
		frames:    make(chan []image.Image, 8),
		raw:       make(chan []image.Image, updateBacklog),
		done:      make(chan struct{}),
		width:     width,
		height:    height,
		mouseKick: make(chan struct{}, 1),
	}
	go c.pump()
	return c
}

// push queues a decoded update, blocking once the backlog is full so that the
// server is slowed down rather than tiles being thrown away. raw is never
// closed, so this can never send on a closed channel.
func (c *Client) push(tiles []image.Image) {
	select {
	case c.raw <- tiles:
	case <-c.done:
	}
}

// pump owns frames: it is the only goroutine that sends on it and the only one
// that closes it. Closing is what lets the UI's "for update := range Frames()"
// loop return and put the kiosk back on the discovery screen; without it a
// dropped session left the UI stuck on a dead one.
func (c *Client) pump() {
	defer close(c.frames)
	for {
		select {
		case <-c.done:
			return
		case tiles := <-c.raw:
			select {
			case c.frames <- tiles:
			case <-c.done:
				return
			}
		}
	}
}

// shutdown signals teardown exactly once.
func (c *Client) shutdown() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.closeChannels != nil {
			c.closeChannels()
		}
	})
}

// Done is closed when the session has ended, for whatever reason.
func (c *Client) Done() <-chan struct{} { return c.done }

// Frames returns the channel of screen updates. Each one is the set of tiles
// from a single server bitmap update, in the order they must be painted.
func (c *Client) Frames() <-chan []image.Image { return c.frames }

// SendKey forwards a key press or release to the remote session.
func (c *Client) SendKey(linuxKeycode int, down bool) error {
	sc := linuxToRDPScanCode(linuxKeycode)
	if sc == 0 {
		return nil
	}
	c.send.Lock()
	defer c.send.Unlock()
	c.g.KeyEvent(sc, isExtendedKey(linuxKeycode), down)
	return nil
}

// SendMouse records a pointer position for the sender goroutine. It never
// blocks: if a move is already pending it is simply superseded.
func (c *Client) SendMouse(x, y int, _ uint16) error {
	c.mouseXY.Store(uint64(uint32(x))<<32 | uint64(uint32(y)))
	select {
	case c.mouseKick <- struct{}{}:
	default:
	}
	return nil
}

// mouseSender forwards the newest pointer position each time one arrives,
// skipping any that were superseded while the previous send was in flight.
func (c *Client) mouseSender() {
	var last uint64 = ^uint64(0)
	for {
		select {
		case <-c.done:
			return
		case <-c.mouseKick:
		}
		xy := c.mouseXY.Load()
		if xy == last {
			continue
		}
		last = xy
		c.send.Lock()
		c.g.MouseMove(int(uint32(xy>>32)), int(uint32(xy)))
		c.send.Unlock()
	}
}

// SendMouseDown sends a mouse button press. button: 1=left, 2=right, 3=middle;
// grdp has no way to send the others.
func (c *Client) SendMouseDown(button, x, y int) {
	if button > 3 {
		return
	}
	c.send.Lock()
	defer c.send.Unlock()
	c.g.MouseDown(grdpButton(button), x, y)
}

// SendMouseUp sends a mouse button release.
func (c *Client) SendMouseUp(button, x, y int) {
	if button > 3 {
		return
	}
	c.send.Lock()
	defer c.send.Unlock()
	c.g.MouseUp(grdpButton(button), x, y)
}

// SendWheel sends wheel notches at (x, y); positive scrolls away from the user,
// which on every desktop means scrolling up.
func (c *Client) SendWheel(notches, x, y int) {
	c.send.Lock()
	defer c.send.Unlock()
	for ; notches > 0; notches-- {
		c.g.MouseWheel(1, x, y)
	}
	for ; notches < 0; notches++ {
		c.g.MouseWheel(-1, x, y)
	}
}

// grdpButton translates this package's button numbering to grdp's.
//
// grdp's switch reads 0 as left, 2 as right and 1 as middle — a numbering that
// matches neither evdev nor RDP nor its own parameter name, and is documented
// nowhere. Passing 1 for left therefore arrived at the server as BUTTON3, so
// every left click was delivered as a middle click: windows highlighted under
// the pointer, and nothing ever activated. Passing 3 for middle fell through to
// the default and was sent as a bare move.
func grdpButton(button int) int {
	switch button {
	case 2:
		return 2 // right
	case 3:
		return 1 // middle
	default:
		return 0 // left
	}
}

// Close disconnects from the RDP server and unblocks anyone ranging over
// Frames. It is safe to call more than once.
//
// The protocol teardown runs on its own goroutine: closing a socket whose peer
// has gone away can block in the TLS layer, and Close is called from the input
// loop, which must keep running so the kiosk stays responsive.
func (c *Client) Close() error {
	c.shutdown()
	if c.g != nil {
		go c.g.Close()
	}
	return nil
}

// bitmapToImage converts a grdp Bitmap tile to an *image.RGBA.
//
// Bitmap.BitsPerPixel is bytes per pixel: grdp has already divided by 8 in
// Bpp(). Tiles that went through the RLE decoder come out top-down with 16-bit
// pixels big-endian RGB565 — verified against a live Windows session, where the
// desktop's #F0F0F0 arrives as the word 0xF79E. Tiles the server sent
// uncompressed (Raw) are the wire format instead: rows bottom-up, each padded to
// a multiple of four bytes, 16-bit pixels little-endian. xrdp sends those
// whenever compression would make a tile bigger — photos, gradients.
//
// The pixels are written straight into Pix: this runs for every tile of every
// update, and image.Set's per-pixel interface dispatch was the single largest
// cost in the session's frame path.
func bitmapToImage(bm client.Bitmap) image.Image {
	w, h := bm.Width, bm.Height
	bpp := bm.BitsPerPixel // bytes per pixel
	data := bm.Data

	// The tile is positioned by DestLeft/DestTop and sized by Width/Height;
	// the Dest* rectangle is not always exactly Width x Height.
	r := image.Rect(bm.DestLeft, bm.DestTop, bm.DestLeft+w, bm.DestTop+h)
	img := image.NewRGBA(r)

	if bpp == 0 || w <= 0 || h <= 0 || len(data) == 0 {
		return img
	}

	srcStride := w * bpp
	if bm.Raw {
		srcStride = (srcStride + 3) &^ 3
	}

	for y := 0; y < h; y++ {
		srcRow := y
		if bm.Raw {
			srcRow = h - 1 - y
		}
		src := srcRow * srcStride
		if src+w*bpp > len(data) {
			continue
		}
		row := data[src : src+w*bpp]
		dst := img.Pix[y*img.Stride : y*img.Stride+w*4]

		switch bpp {
		case 4, 3:
			for x, s := 0, 0; x < w; x, s = x+1, s+bpp {
				d := x * 4
				dst[d+0] = row[s+2]
				dst[d+1] = row[s+1]
				dst[d+2] = row[s]
				dst[d+3] = 0xff
			}
		case 2:
			for x := 0; x < w; x++ {
				var v uint16
				if bm.Raw {
					v = uint16(row[x*2+1])<<8 | uint16(row[x*2])
				} else {
					v = uint16(row[x*2])<<8 | uint16(row[x*2+1])
				}
				d := x * 4
				// Replicating the top bits into the bottom ones maps full
				// intensity to 255 rather than 248, so white stays white.
				r5, g6, b5 := uint8(v>>11), uint8(v>>5)&0x3F, uint8(v)&0x1F
				dst[d+0] = r5<<3 | r5>>2
				dst[d+1] = g6<<2 | g6>>4
				dst[d+2] = b5<<3 | b5>>2
				dst[d+3] = 0xff
			}
		case 1:
			// 8bpp is palettised and we do not track the colour table, so
			// fall back to greyscale rather than render nothing.
			for x := 0; x < w; x++ {
				d := x * 4
				dst[d+0], dst[d+1], dst[d+2], dst[d+3] = row[x], row[x], row[x], 0xff
			}
		}
	}
	return img
}

// linuxToRDPScanCode maps Linux keycodes to RDP (set 1) scan codes. Whether the
// key also needs the extended prefix is isExtendedKey's business.
//
// Linux keycodes 1-88 are the set 1 scancodes themselves, which is why most of
// the table is the identity; the extended keys share a scancode with their
// keypad twin and differ only by the prefix.
func linuxToRDPScanCode(lc int) int {
	switch {
	case lc >= 1 && lc <= 83:
		return lc
	}
	return scanCodes[lc]
}

var scanCodes = map[int]int{
	86: 0x56, // 102nd key: < > | on Turkish Q
	87: 0x57, // F11
	88: 0x58, // F12

	96:  0x1C, // keypad Enter
	97:  0x1D, // right Ctrl
	98:  0x35, // keypad /
	99:  0x37, // Print Screen
	100: 0x38, // right Alt / AltGr
	102: 0x47, // Home
	103: 0x48, // Up
	104: 0x49, // Page Up
	105: 0x4B, // Left
	106: 0x4D, // Right
	107: 0x4F, // End
	108: 0x50, // Down
	109: 0x51, // Page Down
	110: 0x52, // Insert
	111: 0x53, // Delete
	113: 0x20, // Mute
	114: 0x2E, // Volume down
	115: 0x30, // Volume up
	125: 0x5B, // left Windows
	126: 0x5C, // right Windows
	127: 0x5D, // Menu
}

// isExtendedKey reports whether a key is sent with the E0 prefix. Without it the
// server reads the arrow and navigation keys as the numeric keypad, right Ctrl
// as left Ctrl and AltGr as plain Alt — and on a Turkish keyboard AltGr is the
// only way to type @, €, [, ], {, }, \ and |.
func isExtendedKey(lc int) bool {
	switch lc {
	case 96, 97, 98, 99, 100, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111,
		113, 114, 115, 125, 126, 127:
		return true
	}
	return false
}
