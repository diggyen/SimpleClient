package ui

import (
	"image"

	"github.com/kullanici/rdpboot/internal/framebuffer"
)

// Render dispatches to the appropriate screen renderer and blits to fb.
func Render(fb framebuffer.Device, back *image.RGBA, state *UIState, mouseX, mouseY int) {
	switch state.Screen {
	case ScreenDiscovery:
		renderDiscovery(back, state)
	case ScreenModal:
		renderDiscovery(back, state) // modal overlays discovery
		renderModal(back, state)
	case ScreenConnecting:
		renderDiscovery(back, state)
		renderConnecting(back, state)
	case ScreenSession:
		// Session screen: RDP frames are written directly to fb; nothing to render here.
		return
	}

	// Draw mouse cursor on top.
	DrawCursor(back, mouseX, mouseY)

	fb.Blit(back)
}
