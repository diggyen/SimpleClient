package rdp

import (
	"image"
	"image/color"
	"testing"

	"github.com/diggyen/SimpleClient/internal/framebuffer"
)

// tile builds a solid-coloured update tile positioned at r.
func tile(r image.Rectangle, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// A tile must land where the server put it, at its original size. Scaling each
// tile to fill the screen — the old behaviour — made a live session unreadable.
func TestFrameWriter_TileLandsAtItsOwnPosition(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	red := color.RGBA{R: 255, A: 255}
	fw.Write(tile(image.Rect(20, 10, 40, 30), red))

	if got := fb.Img.RGBAAt(25, 15); got != color.Color(red) {
		t.Errorf("inside the tile = %v, want red", got)
	}
	if got := fb.Img.RGBAAt(100, 80); got == red {
		t.Error("the tile was stretched across the screen")
	}
}

// Successive tiles accumulate: writing one must not erase the others.
func TestFrameWriter_TilesAccumulate(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}

	fw.Write(tile(image.Rect(0, 0, 20, 20), red))
	fw.Write(tile(image.Rect(100, 50, 120, 70), green))

	if got := fb.Img.RGBAAt(10, 10); got != color.Color(red) {
		t.Errorf("first tile = %v, want it to survive the second write", got)
	}
	if got := fb.Img.RGBAAt(110, 60); got != color.Color(green) {
		t.Errorf("second tile = %v, want green", got)
	}
}

// A later tile overlapping an earlier one replaces it.
func TestFrameWriter_OverlappingTileWins(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}

	fw.Write(tile(image.Rect(0, 0, 40, 40), red))
	fw.Write(tile(image.Rect(10, 10, 30, 30), blue))

	if got := fb.Img.RGBAAt(20, 20); got != color.Color(blue) {
		t.Errorf("overlap = %v, want the newer blue tile", got)
	}
	if got := fb.Img.RGBAAt(5, 5); got != color.Color(red) {
		t.Errorf("non-overlapping part = %v, want the original red", got)
	}
}

// Tiles that fall outside the screen are clipped, not scaled, and must not panic.
func TestFrameWriter_ClipsOutOfBoundsTiles(t *testing.T) {
	fb := framebuffer.NewMock(100, 100)
	fw := &FrameWriter{FB: fb}

	green := color.RGBA{G: 255, A: 255}
	fw.Write(tile(image.Rect(90, 90, 130, 130), green)) // straddles the edge

	if got := fb.Img.RGBAAt(95, 95); got != color.Color(green) {
		t.Errorf("visible corner = %v, want green", got)
	}
}

func TestFrameWriter_FullyOutsideTileIsIgnored(t *testing.T) {
	fb := framebuffer.NewMock(100, 100)
	fw := &FrameWriter{FB: fb}
	fw.Write(tile(image.Rect(500, 500, 520, 520), color.RGBA{G: 255, A: 255}))
}

func TestFrameWriter_NilFrameIsIgnored(t *testing.T) {
	fw := &FrameWriter{FB: framebuffer.NewMock(100, 100)}
	fw.Write(nil)
}
