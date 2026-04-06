package rdp

import (
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
