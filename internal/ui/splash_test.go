package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/framebuffer"
)

// splashPNG is GRUB's copy of the splash, relative to internal/ui.
const splashPNG = "../../build/splash.png"

// GRUB's picture is the first frame RunSplash draws, so the boot loader's
// still and the spinner after it are one screen. Regenerate it after a change
// to the splash:
//
//	SIMPLECLIENT_SPLASH_UPDATE=1 go test ./internal/ui -run TestSplashPNG
func TestSplashPNG(t *testing.T) {
	want := image.NewRGBA(image.Rect(0, 0, SplashPNGWidth, SplashPNGHeight))
	RenderSplash(want, 0)

	if os.Getenv("SIMPLECLIENT_SPLASH_UPDATE") != "" {
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, want); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(splashPNG, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	f, err := os.Open(splashPNG)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != want.Bounds() {
		t.Fatalf("%s is %v, want %v", splashPNG, got.Bounds(), want.Bounds())
	}
	for y := 0; y < SplashPNGHeight; y++ {
		for x := 0; x < SplashPNGWidth; x++ {
			r, g, b, _ := got.At(x, y).RGBA()
			w := want.RGBAAt(x, y)
			if uint8(r>>8) != w.R || uint8(g>>8) != w.G || uint8(b>>8) != w.B {
				t.Fatalf("%s differs from RenderSplash at (%d,%d); regenerate it (see the comment)", splashPNG, x, y)
			}
		}
	}
}

// The boot command lines make the console's black the splash's background,
// so the console clearing a framebuffer as it takes it over leaves the
// background up rather than a black screen. They have to follow ColorBG.
func TestBootCmdlineConsoleBlackIsSplashBackground(t *testing.T) {
	want := fmt.Sprintf("vt.default_red=%d vt.default_grn=%d vt.default_blu=%d", ColorBG.R, ColorBG.G, ColorBG.B)
	for _, f := range []string{"../../build/grub.cfg", "../../build/slot.cfg"} {
		cfg, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(cfg, []byte(want)) {
			t.Errorf("%s does not set %s", f, want)
		}
	}
}

// The splash fits every screen the kiosk supports, the spinner under the
// wordmark and clear of it.
func TestLayoutSplash_Fits(t *testing.T) {
	for _, s := range [][2]int{{640, 480}, {800, 600}, {1024, 768}, {1280, 800}, {1366, 768}, {1920, 1080}} {
		l := layoutSplash(s[0], s[1])
		screen := image.Rect(0, 0, s[0], s[1])
		for name, r := range map[string]image.Rectangle{"mark": l.Mark, "word": l.Word, "spinner": l.Spinner} {
			if !r.In(screen) {
				t.Errorf("%dx%d: %s %v off the screen", s[0], s[1], name, r)
			}
		}
		if l.Spinner.Min.Y <= l.Mark.Max.Y || l.Spinner.Overlaps(l.Word) {
			t.Errorf("%dx%d: spinner %v runs into the logo (mark %v, word %v)", s[0], s[1], l.Spinner, l.Mark, l.Word)
		}
	}
}

// RunSplash draws the whole splash once and then only the spinner, and
// returns when it is told to stop.
func TestRunSplash_TurnsOnlyTheSpinner(t *testing.T) {
	fb := &rectFB{MockFB: framebuffer.NewMock(1024, 768)}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { RunSplash(fb, stop); close(done) }()
	time.Sleep(3*splashStep + splashStep/2)
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunSplash did not return after stop")
	}

	if fb.blits != 1 {
		t.Errorf("%d full blits, want 1", fb.blits)
	}
	spin := layoutSplash(1024, 768).Spinner
	if len(fb.rects) == 0 {
		t.Fatal("the spinner did not turn in 3.5 steps' time")
	}
	for _, r := range fb.rects {
		if r != spin {
			t.Errorf("redrew %v, want only the spinner %v", r, spin)
		}
	}
	// What is on the screen is a whole splash at the step it got to.
	want := image.NewRGBA(fb.Bounds())
	RenderSplash(want, len(fb.rects))
	if !bytes.Equal(fb.Img.Pix, want.Pix) {
		t.Error("the screen is not the splash at its last step")
	}
}
