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

// --- Pointer ---

// RDP makes the client responsible for the pointer: the server never paints it
// into the screen updates. Without this the remote desktop has no cursor at all.
func TestFrameWriter_MoveCursorPaintsPointer(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	fw.Write(tile(image.Rect(0, 0, 200, 100), color.RGBA{R: 10, G: 10, B: 10, A: 255}))
	fw.MoveCursor(100, 50)

	if got := fb.Img.RGBAAt(100, 50); got != cursorDotColor {
		t.Errorf("cursor centre = %v, want the pointer dot", got)
	}
	if got := fb.Img.RGBAAt(100-cursorArm, 50); got != cursorColor {
		t.Errorf("cursor left arm = %v, want the pointer colour", got)
	}
	if got := fb.Img.RGBAAt(100, 50-cursorArm); got != cursorColor {
		t.Errorf("cursor top arm = %v, want the pointer colour", got)
	}
}

// Moving must restore the desktop underneath, otherwise the pointer smears a
// trail across the session.
func TestFrameWriter_MoveCursorLeavesNoTrail(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	desktop := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	fw.Write(tile(image.Rect(0, 0, 200, 100), desktop))

	fw.MoveCursor(50, 50)
	fw.MoveCursor(150, 50)

	if got := fb.Img.RGBAAt(50, 50); got != desktop {
		t.Errorf("old cursor position = %v, want the desktop restored", got)
	}
	if got := fb.Img.RGBAAt(150, 50); got != cursorDotColor {
		t.Errorf("new cursor position = %v, want the pointer", got)
	}
}

// A screen update covering the pointer paints over it; it has to come back.
func TestFrameWriter_TileOverPointerRedrawsIt(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	fw.Write(tile(image.Rect(0, 0, 200, 100), color.RGBA{R: 10, G: 10, B: 10, A: 255}))
	fw.MoveCursor(100, 50)

	// A tile landing right on top of the pointer.
	fw.Write(tile(image.Rect(90, 40, 110, 60), color.RGBA{B: 200, A: 255}))

	if got := fb.Img.RGBAAt(100, 50); got != cursorDotColor {
		t.Errorf("cursor = %v, want it repainted over the new tile", got)
	}
}

// A tile elsewhere must not disturb the pointer.
func TestFrameWriter_UnrelatedTileLeavesPointerAlone(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	fw.Write(tile(image.Rect(0, 0, 200, 100), color.RGBA{R: 10, G: 10, B: 10, A: 255}))
	fw.MoveCursor(100, 50)
	fw.Write(tile(image.Rect(0, 0, 20, 20), color.RGBA{G: 200, A: 255}))

	if got := fb.Img.RGBAAt(100, 50); got != cursorDotColor {
		t.Errorf("cursor = %v, want it untouched", got)
	}
}

func TestFrameWriter_HideCursorRestoresDesktop(t *testing.T) {
	fb := framebuffer.NewMock(200, 100)
	fw := &FrameWriter{FB: fb}

	desktop := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	fw.Write(tile(image.Rect(0, 0, 200, 100), desktop))
	fw.MoveCursor(100, 50)
	fw.HideCursor()

	if got := fb.Img.RGBAAt(100, 50); got != desktop {
		t.Errorf("after HideCursor = %v, want the desktop restored", got)
	}
}

// The pointer is clipped at the screen edge rather than wrapping or panicking.
func TestFrameWriter_CursorAtEdge(t *testing.T) {
	fb := framebuffer.NewMock(100, 100)
	fw := &FrameWriter{FB: fb}

	fw.Write(tile(image.Rect(0, 0, 100, 100), color.RGBA{R: 10, G: 10, B: 10, A: 255}))
	fw.MoveCursor(0, 0)
	fw.MoveCursor(99, 99)

	if got := fb.Img.RGBAAt(99, 99); got != cursorDotColor {
		t.Errorf("cursor at the bottom-right = %v, want the pointer", got)
	}
}

// MoveCursor before any tile has arrived must still show a pointer.
func TestFrameWriter_CursorBeforeFirstTile(t *testing.T) {
	fb := framebuffer.NewMock(100, 100)
	fw := &FrameWriter{FB: fb}

	fw.MoveCursor(50, 50)

	if got := fb.Img.RGBAAt(50, 50); got != cursorDotColor {
		t.Errorf("cursor = %v, want it drawn even with no canvas yet", got)
	}
}
