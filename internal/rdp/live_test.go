package rdp

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"
	"time"
)

// TestLiveConnect drives a real RDP server end to end: TLS/NLA negotiation,
// authentication, and the bitmap stream that the kiosk paints to the
// framebuffer. Everything else in this package is unit-tested against
// synthetic data, so this is the only check that the grdp handshake actually
// works against Windows.
//
// It is skipped unless a target is configured, because it needs a reachable
// host and real credentials:
//
//	SIMPLECLIENT_RDP_ADDR=10.0.0.5:3389 \
//	SIMPLECLIENT_RDP_USER=administrator \
//	SIMPLECLIENT_RDP_PASS=... \
//	go test -mod=vendor ./internal/rdp -run TestLiveConnect -v
//
// SIMPLECLIENT_RDP_DOMAIN is optional.
func TestLiveConnect(t *testing.T) {
	addr := os.Getenv("SIMPLECLIENT_RDP_ADDR")
	if addr == "" {
		t.Skip("SIMPLECLIENT_RDP_ADDR not set; skipping live RDP test")
	}

	creds := Credentials{
		Username: os.Getenv("SIMPLECLIENT_RDP_USER"),
		Password: os.Getenv("SIMPLECLIENT_RDP_PASS"),
		Domain:   os.Getenv("SIMPLECLIENT_RDP_DOMAIN"),
	}

	const width, height = 1024, 768

	c, err := New(addr, creds, width, height)
	if err != nil {
		t.Fatalf("connecting to %s: %v", addr, err)
	}
	defer c.Close()

	// A connected session starts painting immediately. Waiting for frames
	// rather than just a successful login is what proves the bitmap decoding
	// path works: a session that authenticates but decodes nothing would still
	// leave the kiosk on a black screen.
	deadline := time.After(30 * time.Second)
	var (
		frames int
		bounds image.Rectangle
	)

	for frames < 3 {
		select {
		case img, ok := <-c.Frames():
			if !ok {
				t.Fatalf("frame channel closed after %d frames", frames)
			}
			if img == nil {
				t.Fatal("received a nil frame")
			}
			b := img.Bounds()
			if b.Dx() <= 0 || b.Dy() <= 0 {
				t.Fatalf("frame %d has empty bounds %v", frames, b)
			}
			bounds = bounds.Union(b)
			frames++

		case <-deadline:
			t.Fatalf("only received %d frames in 30s; expected the server to paint the session", frames)
		}
	}

	t.Logf("connected to %s, received %d frames covering %v", addr, frames, bounds)

	// SIMPLECLIENT_RDP_SHOT composites everything that arrives over the next few
	// seconds into one PNG. It is the only way to see what the kiosk would
	// actually paint: a session can be live and still send nothing this decoder
	// understands, which looks identical to a black screen.
	if shot := os.Getenv("SIMPLECLIENT_RDP_SHOT"); shot != "" {
		writeCompositeScreenshot(t, c, shot, width, height)
	}

	// Input forwarding shares the same connection; a failure here means a live
	// session would accept no keyboard or mouse.
	if err := c.SendKey(30 /* KEY_A */, true); err != nil {
		t.Errorf("SendKey down: %v", err)
	}
	if err := c.SendKey(30, false); err != nil {
		t.Errorf("SendKey up: %v", err)
	}
	if err := c.SendMouse(width/2, height/2, 0); err != nil {
		t.Errorf("SendMouse: %v", err)
	}
}

// writeCompositeScreenshot drains frames for a few seconds, draws each tile onto
// a full-desktop canvas and saves the result, reporting how much of the desktop
// the server actually painted.
func writeCompositeScreenshot(t *testing.T, c *Client, path string, width, height int) {
	t.Helper()

	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	painted := image.Rectangle{}
	tiles := 0

	timeout := time.After(10 * time.Second)
collect:
	for {
		select {
		case img, ok := <-c.Frames():
			if !ok {
				break collect
			}
			b := img.Bounds()
			draw.Draw(canvas, b, img, b.Min, draw.Src)
			painted = painted.Union(b)
			tiles++
		case <-timeout:
			break collect
		}
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, canvas); err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}

	coverage := 0.0
	if width*height > 0 {
		coverage = 100 * float64(painted.Dx()*painted.Dy()) / float64(width*height)
	}
	t.Logf("composited %d tiles into %s; painted region %v covers %.1f%% of the desktop",
		tiles, path, painted, coverage)
}
