package framebuffer

import (
	"image"
	"image/color"
)

// MockFB is an in-memory framebuffer for testing without /dev/fb0.
type MockFB struct {
	Img    *image.RGBA
	width  int
	height int
}

// NewMock creates a MockFB with the given dimensions.
func NewMock(width, height int) *MockFB {
	return &MockFB{
		Img:    image.NewRGBA(image.Rect(0, 0, width, height)),
		width:  width,
		height: height,
	}
}

func (m *MockFB) Bounds() image.Rectangle        { return m.Img.Bounds() }
func (m *MockFB) Width() int                     { return m.width }
func (m *MockFB) Height() int                    { return m.height }
func (m *MockFB) Blit(img *image.RGBA)           { copy(m.Img.Pix, img.Pix) }
func (m *MockFB) BlitRect(img *image.RGBA, r image.Rectangle) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			m.Img.SetRGBA(x, y, img.RGBAAt(x, y))
		}
	}
}
func (m *MockFB) WritePixel(x, y int, c color.RGBA) { m.Img.SetRGBA(x, y, c) }
func (m *MockFB) Close() error                       { return nil }
