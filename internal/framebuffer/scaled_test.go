package framebuffer

import (
	"image"
	"image/color"
	"testing"
)

func TestScale(t *testing.T) {
	for _, c := range []struct{ w, h, want int }{
		{1024, 768, 1}, {1920, 1080, 1}, {2560, 1440, 1}, {3440, 1440, 1},
		{3840, 2160, 2}, {5120, 2880, 2}, {7680, 4320, 4}, {640, 480, 1},
	} {
		if got := Scale(c.w, c.h); got != c.want {
			t.Errorf("Scale(%d, %d) = %d, want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestScaledDrawsSquares(t *testing.T) {
	m := NewMock(9, 7) // the odd column and row are outside the scaled screen
	s := NewScaled(m, 2)
	if s.Width() != 4 || s.Height() != 3 {
		t.Fatalf("scaled size %dx%d", s.Width(), s.Height())
	}
	img := image.NewRGBA(s.Bounds())
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 7, 255})
		}
	}
	s.Blit(img)
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			want := color.RGBA{uint8(x / 2), uint8(y / 2), 7, 255}
			if got := m.Img.RGBAAt(x, y); got != want {
				t.Fatalf("screen (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
	if got := m.Img.RGBAAt(8, 6); got != (color.RGBA{}) {
		t.Fatalf("drawn outside the scaled screen: %v", got)
	}

	// A rectangle redraws just its squares.
	img.SetRGBA(1, 1, color.RGBA{200, 0, 0, 255})
	img.SetRGBA(3, 2, color.RGBA{0, 200, 0, 255})
	s.BlitRect(img, image.Rect(1, 1, 2, 2))
	if got := m.Img.RGBAAt(3, 3); got != (color.RGBA{200, 0, 0, 255}) {
		t.Fatalf("rectangle not drawn: %v", got)
	}
	if got := m.Img.RGBAAt(6, 4); got != (color.RGBA{3, 2, 7, 255}) {
		t.Fatalf("drawn outside the rectangle: %v", got)
	}

	s.WritePixel(0, 2, color.RGBA{9, 9, 9, 255})
	for _, p := range []image.Point{{0, 4}, {1, 4}, {0, 5}, {1, 5}} {
		if got := m.Img.RGBAAt(p.X, p.Y); got != (color.RGBA{9, 9, 9, 255}) {
			t.Fatalf("pixel square at %v = %v", p, got)
		}
	}
}

func TestScaledOfOneIsTheDevice(t *testing.T) {
	m := NewMock(4, 4)
	if NewScaled(m, 1) != Device(m) {
		t.Fatal("a factor of 1 wrapped the device")
	}
}
