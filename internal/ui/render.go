package ui

import (
	"image"
	"image/draw"

	"github.com/diggyen/SimpleClient/internal/framebuffer"
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
	case ScreenAuto:
		renderDiscovery(back, state)
		renderAuto(back, state)
	case ScreenInstall:
		renderDiscovery(back, state)
		renderInstall(back, state)
	case ScreenNetwork:
		renderDiscovery(back, state)
		renderNet(back, state)
	case ScreenWifi:
		renderDiscovery(back, state)
		renderWifi(back, state)
	case ScreenUpdate:
		renderDiscovery(back, state)
		renderUpdate(back, state)
	case ScreenPower:
		renderDiscovery(back, state)
		renderPower(back, state)
	case ScreenSession:
		// Session screen: RDP frames are written directly to fb; nothing to render here.
		return
	}

	// Draw mouse cursor on top.
	state.drawnPointer.draw(back, mouseX, mouseY)

	fb.Blit(back)
}

// MovePointer moves the pointer over the screen Render last drew in back to
// (x, y), and puts on fb only the squares it left and now covers. Moving the
// mouse changes nothing else, and redrawing the whole list for it took 8 ms a
// frame at 1366x768, 13 at 1080p and 23 at 4K on a fast desktop processor:
// the pointer dragged on a kiosk's.
func MovePointer(fb framebuffer.Device, back *image.RGBA, state *UIState, x, y int) {
	p := &state.drawnPointer
	if !p.drawn || (p.x == x && p.y == y) {
		return
	}
	left := p.at
	p.restore(back)
	p.draw(back, x, y)
	fb.BlitRect(back, left)
	fb.BlitRect(back, p.at)
	if f, ok := fb.(interface{ Flush() }); ok {
		f.Flush()
	}
}

// softPointer is the pointer drawn into a frame, with the pixels it covers, so
// moving it puts back what was under it rather than redrawing the screen.
type softPointer struct {
	drawn bool
	x, y  int
	at    image.Rectangle // where it is drawn, within the frame
	under *image.RGBA     // what was at at before it
}

func (p *softPointer) draw(img *image.RGBA, x, y int) {
	if p.under == nil {
		p.under = image.NewRGBA(image.Rect(0, 0, len(cursorSprite[0]), len(cursorSprite)))
	}
	p.drawn, p.x, p.y = true, x, y
	p.at = image.Rect(x, y, x+p.under.Rect.Dx(), y+p.under.Rect.Dy()).Intersect(img.Bounds())
	draw.Draw(p.under, p.under.Rect, img, p.at.Min, draw.Src)
	DrawCursor(img, x, y)
}

func (p *softPointer) restore(img *image.RGBA) {
	draw.Draw(img, p.at, p.under, image.Point{}, draw.Src)
}
