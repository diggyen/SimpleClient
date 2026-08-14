package rdp

import (
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"

	"github.com/tomatome/grdp/client"
	"github.com/tomatome/grdp/glog"
)

// Credentials holds RDP login credentials.
type Credentials struct {
	Username string
	Password string
	Domain   string
}

// connectTimeout bounds the whole handshake: TCP dial, x224, MCS, licensing and
// capability exchange. Ten seconds is generous for a LAN and short enough that a
// wrong password does not leave the kiosk staring at a spinner.
const connectTimeout = 10 * time.Second

// Client wraps grdp/client and provides frame streaming and input forwarding.
type Client struct {
	g      *client.Client
	frames chan image.Image
	done   chan struct{}
	width  int
	height int

	// mu guards closed and serialises sends on frames against closing it.
	mu     sync.Mutex
	closed bool
}

// New creates and connects an RDP client. Times out after 10 seconds.
func New(addr string, creds Credentials, width, height int) (*Client, error) {
	setting := client.NewSetting()
	setting.Width = width
	setting.Height = height
	setting.LogLevel = glog.WARN

	// For domain authentication pass "domain\user" as the user string.
	user := creds.Username
	if creds.Domain != "" {
		user = creds.Domain + "\\" + creds.Username
	}

	g := client.NewClient(addr, user, creds.Password, client.TC_RDP, setting)

	c := &Client{
		g:      g,
		frames: make(chan image.Image, 4),
		done:   make(chan struct{}),
		width:  width,
		height: height,
	}

	// Login has to come first. grdp's RdpClient is an empty struct until Login
	// builds the protocol stack, and every On* registration dereferences the
	// pdu client it creates there — attaching handlers beforehand panics with a
	// nil pointer. Login is not the blocking call its name suggests: it dials,
	// starts the x224 handshake and returns, leaving the session running on
	// grdp's own reader goroutine.
	if err := g.Login(); err != nil {
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
	g.OnReady(func() {
		select {
		case readyCh <- struct{}{}:
		default:
		}
	})

	g.OnError(func(e error) {
		select {
		case errCh <- e:
		default:
		}
	})

	g.OnClose(c.shutdown)

	// Register bitmap callback — grdp calls this for every screen update tile.
	g.OnBitmap(func(bitmaps []client.Bitmap) {
		for _, bm := range bitmaps {
			c.push(bitmapToImage(bm))
		}
	})

	// Wait for the session to come up, fail, or time out.
	select {
	case <-readyCh:
		return c, nil
	case err := <-errCh:
		g.Close()
		return nil, fmt.Errorf("RDP connect %s: %w", addr, err)
	case <-time.After(connectTimeout):
		g.Close()
		return nil, fmt.Errorf("RDP connect %s: timeout after %s", addr, connectTimeout)
	}
}

// push hands a decoded tile to the frame consumer, dropping the oldest frame
// when the consumer has fallen behind. Sends are serialised with shutdown so a
// tile arriving during teardown cannot be written to a closed channel.
func (c *Client) push(img image.Image) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.frames <- img:
	default:
		select {
		case <-c.frames:
		default:
		}
		select {
		case c.frames <- img:
		default:
		}
	}
}

// shutdown closes the frame channel exactly once. Closing it is what lets the
// UI's "for frame := range client.Frames()" loop return and put the kiosk back
// on the discovery screen; without it a dropped session left the UI stuck.
func (c *Client) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
	close(c.frames)
}

// Frames returns the channel of incoming video frames.
func (c *Client) Frames() <-chan image.Image { return c.frames }

// SendKey forwards a key press or release to the remote session.
func (c *Client) SendKey(linuxKeycode int, down bool) error {
	sc := linuxToRDPScanCode(linuxKeycode)
	if sc == 0 {
		return nil
	}
	name := fmt.Sprintf("sc_%02x", sc)
	if down {
		c.g.KeyDown(sc, name)
	} else {
		c.g.KeyUp(sc, name)
	}
	return nil
}

// SendMouse forwards a mouse movement to the remote session.
func (c *Client) SendMouse(x, y int, _ uint16) error {
	c.g.MouseMove(x, y)
	return nil
}

// SendMouseDown sends a mouse button press. button: 1=left, 2=right, 3=middle.
func (c *Client) SendMouseDown(button, x, y int) {
	c.g.MouseDown(button, x, y)
}

// SendMouseUp sends a mouse button release.
func (c *Client) SendMouseUp(button, x, y int) {
	c.g.MouseUp(button, x, y)
}

// Close disconnects from the RDP server and unblocks anyone ranging over
// Frames. It is safe to call more than once.
func (c *Client) Close() error {
	c.g.Close()
	c.shutdown()
	return nil
}

// bitmapToImage converts a grdp Bitmap tile to image.Image.
//
// Bitmap.BitsPerPixel is bytes per pixel: grdp has already divided by 8 in
// Bpp(). Rows run top-down and 16-bit pixels are big-endian RGB565 — both
// verified against a live Windows session, where the desktop's #F0F0F0 arrives
// as the word 0xF79E. Reading those little-endian, or flipping the rows, turns
// the remote desktop into upside-down text in the wrong colours.
func bitmapToImage(bm client.Bitmap) image.Image {
	w, h := bm.Width, bm.Height
	bpp := bm.BitsPerPixel // bytes per pixel
	data := bm.Data

	// The tile is positioned by DestLeft/DestTop and sized by Width/Height;
	// the Dest* rectangle is not always exactly Width x Height.
	r := image.Rect(bm.DestLeft, bm.DestTop, bm.DestLeft+w, bm.DestTop+h)
	img := image.NewNRGBA(r)

	if bpp == 0 || len(data) == 0 {
		return img
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * bpp
			if off+bpp > len(data) {
				break
			}
			var r2, g2, b2 uint8
			switch bpp {
			case 4, 3:
				b2 = data[off]
				g2 = data[off+1]
				r2 = data[off+2]
			case 2:
				v := uint16(data[off])<<8 | uint16(data[off+1])
				r2 = uint8(v & 0xF800 >> 8)
				g2 = uint8(v & 0x07E0 >> 3)
				b2 = uint8(v & 0x001F << 3)
			case 1:
				// 8bpp is palettised and we do not track the colour table, so
				// fall back to greyscale rather than render nothing.
				r2 = data[off]
				g2 = r2
				b2 = r2
			}
			img.SetNRGBA(r.Min.X+x, r.Min.Y+y, color.NRGBA{R: r2, G: g2, B: b2, A: 255})
		}
	}
	return img
}

// linuxToRDPScanCode maps Linux keycodes to RDP scan codes.
func linuxToRDPScanCode(lc int) int {
	table := map[int]int{
		1: 0x01, 2: 0x02, 3: 0x03, 4: 0x04, 5: 0x05, 6: 0x06,
		7: 0x07, 8: 0x08, 9: 0x09, 10: 0x0A, 11: 0x0B, 12: 0x0C,
		13: 0x0D, 14: 0x0E, 15: 0x0F, 16: 0x10, 17: 0x11, 18: 0x12,
		19: 0x13, 20: 0x14, 21: 0x15, 22: 0x16, 23: 0x17, 24: 0x18,
		25: 0x19, 26: 0x1A, 27: 0x1B, 28: 0x1C, 29: 0x1D, 30: 0x1E,
		31: 0x1F, 32: 0x20, 33: 0x21, 34: 0x22, 35: 0x23, 36: 0x24,
		37: 0x25, 38: 0x26, 39: 0x27, 40: 0x28, 41: 0x29, 42: 0x2A,
		43: 0x2B, 44: 0x2C, 45: 0x2D, 46: 0x2E, 47: 0x2F, 48: 0x30,
		49: 0x31, 50: 0x32, 51: 0x33, 52: 0x34, 53: 0x35, 54: 0x36,
		55: 0x37, 56: 0x38, 57: 0x39, 58: 0x3A,
		59: 0x3B, 60: 0x3C, 61: 0x3D, 62: 0x3E, 63: 0x3F, 64: 0x40,
		65: 0x41, 66: 0x42, 67: 0x43, 68: 0x44,
		102: 0x47, 103: 0x48, 104: 0x49,
		105: 0x4B, 106: 0x4D, 107: 0x4F,
		108: 0x50, 109: 0x51, 110: 0x52, 111: 0x53,
	}
	return table[lc]
}
