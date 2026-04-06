package framebuffer

import (
	"image"
	"image/color"
)

// Device is the interface that both the real FB and MockFB satisfy.
// All UI and render code depends on this interface, not the concrete type.
type Device interface {
	Blit(img *image.RGBA)
	BlitRect(img *image.RGBA, r image.Rectangle)
	WritePixel(x, y int, c color.RGBA)
	Bounds() image.Rectangle
	Width() int
	Height() int
	Close() error
}
