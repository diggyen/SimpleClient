package rdp

import (
	"fmt"
	"image"
	"image/color"
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

// Client wraps grdp/client and provides frame streaming and input forwarding.
type Client struct {
	g      *client.Client
	frames chan image.Image
	done   chan struct{}
	width  int
	height int
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

	successCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)

	g.OnSuccess(func() {
		select {
		case successCh <- struct{}{}:
		default:
		}
	})

	g.OnError(func(e error) {
		select {
		case errCh <- e:
		default:
		}
	})

	g.OnClose(func() {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	})

	// Register bitmap callback — grdp calls this for every screen update tile.
	g.OnBitmap(func(bitmaps []client.Bitmap) {
		for _, bm := range bitmaps {
			img := bitmapToImage(bm)
			select {
			case c.frames <- img:
			default:
				// Drop oldest frame to avoid blocking.
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
	})

	// Login blocks until session ends; run in goroutine.
	go func() {
		if err := g.Login(); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	// Wait for connection success, error, or timeout.
	select {
	case <-successCh:
		return c, nil
	case err := <-errCh:
		return nil, fmt.Errorf("RDP connect %s: %w", addr, err)
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("RDP connect %s: timeout after 10s", addr)
	}
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

// Close disconnects from the RDP server.
// Note: grdp's client.Client does not expose a Close method on the outer
// type; Close() is on the internal Control interface.  We signal closure
// via the done channel so consumers (Frames loop) unblock.
func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return nil
}

// bitmapToImage converts a grdp Bitmap tile to image.Image.
// Bitmap.BitsPerPixel is bytes-per-pixel (grdp already divides by 8 via Bpp()).
func bitmapToImage(bm client.Bitmap) image.Image {
	r := image.Rect(bm.DestLeft, bm.DestTop, bm.DestRight+1, bm.DestBottom+1)
	img := image.NewNRGBA(r)

	w := bm.Width
	h := bm.Height
	bpp := bm.BitsPerPixel // bytes per pixel
	data := bm.Data

	if bpp == 0 || len(data) == 0 {
		return img
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Bitmap is stored bottom-up with BGR byte order.
			off := ((h - 1 - y) * w + x) * bpp
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
				lo := data[off]
				hi := data[off+1]
				v := uint16(hi)<<8 | uint16(lo)
				r2 = uint8((v >> 11) << 3)
				g2 = uint8((v>>5&0x3f) << 2)
				b2 = uint8((v & 0x1f) << 3)
			case 1:
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
