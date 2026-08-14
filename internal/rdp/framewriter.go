package rdp

import (
	"image"
	"image/color"
	"image/draw"
	"sync"

	"github.com/diggyen/SimpleClient/internal/framebuffer"
)

// Pointer geometry, kept identical to ui.DrawCursor / ui.ColorCursor so the
// pointer looks the same inside a session as it does on the host list.
// internal/ui imports this package, so the shape cannot live in one place
// without an import cycle.
const cursorArm = 6

var (
	cursorColor    = color.RGBA{R: 255, G: 255, B: 100, A: 255}
	cursorDotColor = color.RGBA{R: 255, G: 80, B: 80, A: 255}
)

// FrameWriter composites RDP screen updates onto the framebuffer.
//
// RDP does not send whole screens: it sends small tiles, each carrying its own
// position on the remote desktop. They have to be accumulated on a persistent
// canvas — scaling every individual tile up to the full framebuffer, which is
// what this used to do, blew a 64x64 fragment across the entire display and made
// a connected session unusable.
//
// It also draws the mouse pointer. RDP makes the *client* responsible for that:
// the server moves its own pointer but never paints it into the bitmap stream,
// so a session with no local pointer leaves the user pushing an invisible mouse
// around.
type FrameWriter struct {
	FB framebuffer.Device

	// mu guards everything below. Write runs on the RDP frame goroutine while
	// MoveCursor runs on the UI input loop.
	mu sync.Mutex

	// canvas holds the assembled desktop, without the pointer drawn on it, so
	// the pixels under the pointer can be restored when it moves.
	canvas *image.RGBA

	cursorX, cursorY int
	cursorVisible    bool
}

// Write draws one screen update tile and pushes just that region to the screen.
func (fw *FrameWriter) Write(img image.Image) {
	if img == nil {
		return
	}

	fw.mu.Lock()
	defer fw.mu.Unlock()

	screen := fw.FB.Bounds()
	if fw.canvas == nil {
		fw.canvas = image.NewRGBA(screen)
	}

	// Tiles are positioned in remote-desktop coordinates. The session is opened
	// at the framebuffer's own resolution, so they line up 1:1; anything outside
	// the screen (a server that ignored the requested size) is clipped rather
	// than scaled, which keeps text sharp.
	r := img.Bounds().Intersect(screen)
	if r.Empty() {
		return
	}

	draw.Draw(fw.canvas, r, img, r.Min, draw.Src)

	// Only the touched rectangle goes to the framebuffer. A full-screen blit per
	// tile would be roughly a thousand times the work for a busy desktop.
	fw.FB.BlitRect(fw.canvas, r)

	// A tile that covers the pointer has just painted over it.
	if fw.cursorVisible && r.Overlaps(fw.cursorRect()) {
		fw.paintCursor()
	}
}

// MoveCursor repaints the mouse pointer at (x, y), restoring whatever the
// desktop has underneath its previous position.
func (fw *FrameWriter) MoveCursor(x, y int) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.cursorVisible && fw.canvas != nil {
		// Repaint the desktop where the pointer used to be, so it does not
		// leave a trail across the session.
		fw.FB.BlitRect(fw.canvas, fw.cursorRect())
	}

	fw.cursorX, fw.cursorY = x, y
	fw.cursorVisible = true
	fw.paintCursor()
}

// HideCursor erases the pointer, restoring the desktop underneath. Used when a
// session ends and the kiosk UI takes the screen back.
func (fw *FrameWriter) HideCursor() {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.cursorVisible && fw.canvas != nil {
		fw.FB.BlitRect(fw.canvas, fw.cursorRect())
	}
	fw.cursorVisible = false
}

// cursorRect is the screen area the pointer covers. Callers must hold mu.
func (fw *FrameWriter) cursorRect() image.Rectangle {
	r := image.Rect(
		fw.cursorX-cursorArm, fw.cursorY-cursorArm,
		fw.cursorX+cursorArm+1, fw.cursorY+cursorArm+1,
	)
	return r.Intersect(fw.FB.Bounds())
}

// paintCursor draws the crosshair straight to the framebuffer, leaving the
// canvas untouched. Callers must hold mu.
func (fw *FrameWriter) paintCursor() {
	r := fw.cursorRect()
	if r.Empty() {
		return
	}
	for x := r.Min.X; x < r.Max.X; x++ {
		fw.FB.WritePixel(x, fw.cursorY, cursorColor)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		fw.FB.WritePixel(fw.cursorX, y, cursorColor)
	}
	fw.FB.WritePixel(fw.cursorX, fw.cursorY, cursorDotColor)
}
