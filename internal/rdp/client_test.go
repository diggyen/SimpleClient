package rdp

import (
	"image"
	"image/color"
	"testing"

	"github.com/tomatome/grdp/client"
)

func TestLinuxToRDPScanCode(t *testing.T) {
	tests := []struct {
		linux int
		rdp   int
	}{
		{1, 0x01},   // ESC
		{28, 0x1C},  // ENTER
		{14, 0x0E},  // BACKSPACE
		{15, 0x0F},  // TAB
		{57, 0x39},  // SPACE
		{30, 0x1E},  // A
		{48, 0x30},  // B
		{2, 0x02},   // 1
		{11, 0x0B},  // 0
		{42, 0x2A},  // Left Shift
		{29, 0x1D},  // Left Ctrl
		{56, 0x38},  // Left Alt
		{103, 0x48}, // Up
		{108, 0x50}, // Down
		{105, 0x4B}, // Left
		{106, 0x4D}, // Right
	}
	for _, tt := range tests {
		got := linuxToRDPScanCode(tt.linux)
		if got != tt.rdp {
			t.Errorf("linuxToRDPScanCode(%d) = 0x%02x, want 0x%02x", tt.linux, got, tt.rdp)
		}
	}
}

func TestLinuxToRDPScanCode_Unknown(t *testing.T) {
	got := linuxToRDPScanCode(9999)
	if got != 0 {
		t.Errorf("expected 0 for unknown keycode, got %d", got)
	}
}

func TestBitmapToImage_Empty(t *testing.T) {
	bm := client.Bitmap{
		DestLeft:     0,
		DestTop:      0,
		DestRight:    0,
		DestBottom:   0,
		Width:        0,
		Height:       0,
		BitsPerPixel: 0,
		Data:         nil,
	}
	img := bitmapToImage(bm)
	if img == nil {
		t.Fatal("bitmapToImage should return non-nil even for empty bitmaps")
	}
}

func TestBitmapToImage_4bpp(t *testing.T) {
	// The bitmap rectangle uses DestRight/DestBottom as inclusive bounds,
	// so DestRight=1, DestBottom=1 means a 2x2 pixel area.
	w, h := 2, 2
	data := []byte{
		// bottom-up row 1 (top visual): BGRx
		0xFF, 0x00, 0x00, 0xFF, // pixel (0,1) blue
		0x00, 0xFF, 0x00, 0xFF, // pixel (1,1) green
		// bottom-up row 0 (bottom visual):
		0x00, 0x00, 0xFF, 0xFF, // pixel (0,0) red
		0xFF, 0xFF, 0xFF, 0xFF, // pixel (1,0) white
	}
	bm := client.Bitmap{
		DestLeft:     0,
		DestTop:      0,
		DestRight:    1, // inclusive
		DestBottom:   1, // inclusive
		Width:        w,
		Height:       h,
		BitsPerPixel: 4,
		Data:         data,
	}
	img := bitmapToImage(bm)
	if img == nil {
		t.Fatal("bitmapToImage returned nil")
	}
	// bitmapToImage uses DestRight+1, DestBottom+1 for the rect.
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("expected 2x2 image, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

// The 16bpp path is big-endian RGB565 and rows run top-down. Both were wrong
// originally, which rendered a live session as upside-down text in false
// colours. 0xF79E is the word a real Windows desktop sends for its #F0F0F0
// background, so it doubles as a regression anchor.
func TestBitmapToImage_16bppBigEndianRGB565(t *testing.T) {
	// Two pixels: #F0F0F0 (0xF79E) and pure blue (0x001F), stored big-endian.
	bm := client.Bitmap{
		DestLeft: 0, DestTop: 0,
		DestRight: 1, DestBottom: 0,
		Width: 2, Height: 1,
		BitsPerPixel: 2,
		Data:         []byte{0xF7, 0x9E, 0x00, 0x1F},
	}
	img := bitmapToImage(bm).(*image.NRGBA)

	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{R: 240, G: 240, B: 240, A: 255}) {
		t.Errorf("0xF79E decoded to %v, want the #F0F0F0 desktop grey", got)
	}
	if got := img.NRGBAAt(1, 0); got != (color.NRGBA{R: 0, G: 0, B: 248, A: 255}) {
		t.Errorf("0x001F decoded to %v, want blue", got)
	}
}

// Rows must not be flipped: the first row of data belongs at the top of the tile.
func TestBitmapToImage_RowsAreTopDown(t *testing.T) {
	// 1x2 tile, 32bpp BGRx: first row red, second row green.
	bm := client.Bitmap{
		DestLeft: 0, DestTop: 0,
		DestRight: 0, DestBottom: 1,
		Width: 1, Height: 2,
		BitsPerPixel: 4,
		Data: []byte{
			0x00, 0x00, 0xFF, 0xFF, // row 0: red
			0x00, 0xFF, 0x00, 0xFF, // row 1: green
		},
	}
	img := bitmapToImage(bm).(*image.NRGBA)

	if got := img.NRGBAAt(0, 0); got.R != 255 || got.G != 0 {
		t.Errorf("top row = %v, want red — rows are being flipped", got)
	}
	if got := img.NRGBAAt(0, 1); got.G != 255 || got.R != 0 {
		t.Errorf("bottom row = %v, want green — rows are being flipped", got)
	}
}

// The tile is placed by DestLeft/DestTop and sized by Width/Height.
func TestBitmapToImage_PositionedByDest(t *testing.T) {
	bm := client.Bitmap{
		DestLeft: 64, DestTop: 32,
		DestRight: 65, DestBottom: 33,
		Width: 2, Height: 2,
		BitsPerPixel: 2,
		Data:         make([]byte, 2*2*2),
	}
	got := bitmapToImage(bm).Bounds()
	want := image.Rect(64, 32, 66, 34)
	if got != want {
		t.Errorf("bounds = %v, want %v", got, want)
	}
}
