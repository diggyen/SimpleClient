package rdp

import (
	"image"
	"image/draw"

	"github.com/diggyen/SimpleClient/internal/framebuffer"
)

// FrameWriter composites RDP screen updates onto the framebuffer.
//
// RDP does not send whole screens: it sends small tiles, each carrying its own
// position on the remote desktop. They have to be accumulated on a persistent
// canvas — scaling every individual tile up to the full framebuffer, which is
// what this used to do, blew a 64x64 fragment across the entire display and made
// a connected session unusable.
type FrameWriter struct {
	FB framebuffer.Device

	// canvas holds the assembled desktop between updates.
	canvas *image.RGBA
}

// Write draws one screen update tile and pushes just that region to the screen.
func (fw *FrameWriter) Write(img image.Image) {
	if img == nil {
		return
	}

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
}
