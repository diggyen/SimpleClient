package ui

import (
	"image"
	"image/color"
)

// FillRect fills a rectangle with a solid color.
func FillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// DrawBorder draws a 1-pixel border around rect r.
func DrawBorder(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	DrawHLine(img, r.Min.X, r.Max.X, r.Min.Y, c) // top
	DrawHLine(img, r.Min.X, r.Max.X, r.Max.Y-1, c) // bottom
	DrawVLine(img, r.Min.X, r.Min.Y, r.Max.Y, c) // left
	DrawVLine(img, r.Max.X-1, r.Min.Y, r.Max.Y, c) // right
}

// DrawHLine draws a horizontal line from x1 to x2 (exclusive) at y.
func DrawHLine(img *image.RGBA, x1, x2, y int, c color.RGBA) {
	b := img.Bounds()
	if y < b.Min.Y || y >= b.Max.Y {
		return
	}
	if x1 < b.Min.X {
		x1 = b.Min.X
	}
	if x2 > b.Max.X {
		x2 = b.Max.X
	}
	for x := x1; x < x2; x++ {
		img.SetRGBA(x, y, c)
	}
}

// DrawVLine draws a vertical line from y1 to y2 (exclusive) at x.
func DrawVLine(img *image.RGBA, x, y1, y2 int, c color.RGBA) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X {
		return
	}
	if y1 < b.Min.Y {
		y1 = b.Min.Y
	}
	if y2 > b.Max.Y {
		y2 = b.Max.Y
	}
	for y := y1; y < y2; y++ {
		img.SetRGBA(x, y, c)
	}
}

// DrawProgressBar renders a horizontal progress bar inside rect r.
// pct should be in [0.0, 1.0].
func DrawProgressBar(img *image.RGBA, r image.Rectangle, pct float64, fg, bg color.RGBA) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	FillRect(img, r, bg)
	filled := image.Rect(r.Min.X, r.Min.Y, r.Min.X+int(float64(r.Dx())*pct), r.Max.Y)
	FillRect(img, filled, fg)
}

// DrawCursor draws a small crosshair mouse cursor at (x, y).
func DrawCursor(img *image.RGBA, x, y int) {
	c := ColorCursor
	size := 6
	DrawHLine(img, x-size, x+size+1, y, c)
	DrawVLine(img, x, y-size, y+size+1, c)
	// Inner dot for visibility.
	if x >= 0 && y >= 0 && x < img.Bounds().Max.X && y < img.Bounds().Max.Y {
		img.SetRGBA(x, y, color.RGBA{R: 255, G: 80, B: 80, A: 255})
	}
}

// DrawInputField renders a text input field. The active field gets an accent border.
func DrawInputField(img *image.RGBA, r image.Rectangle, text string, active bool) {
	// Background
	FillRect(img, r, ColorPanel)

	// Border color depends on focus.
	borderColor := ColorBorder
	if active {
		borderColor = ColorFocus
	}
	DrawBorder(img, r, borderColor)

	// Text with cursor when active.
	display := text
	if active {
		display += "_"
	}
	maxChars := (r.Dx() - 8) / CharW
	if maxChars < 1 {
		maxChars = 1
	}
	runes := []rune(display)
	if len(runes) > maxChars {
		runes = runes[len(runes)-maxChars:]
	}
	DrawText(img, r.Min.X+4, r.Min.Y+3, string(runes), ColorText)
}

// BlendPixel blends src over dst using src.A as alpha.
func BlendPixel(img *image.RGBA, x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= img.Bounds().Max.X || y >= img.Bounds().Max.Y {
		return
	}
	if c.A == 255 {
		img.SetRGBA(x, y, c)
		return
	}
	dst := img.RGBAAt(x, y)
	alpha := uint32(c.A)
	invAlpha := 255 - alpha
	out := color.RGBA{
		R: uint8((uint32(c.R)*alpha + uint32(dst.R)*invAlpha) / 255),
		G: uint8((uint32(c.G)*alpha + uint32(dst.G)*invAlpha) / 255),
		B: uint8((uint32(c.B)*alpha + uint32(dst.B)*invAlpha) / 255),
		A: 255,
	}
	img.SetRGBA(x, y, out)
}

// FillRectBlend fills a rectangle with a color, blending via alpha.
func FillRectBlend(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			BlendPixel(img, x, y, c)
		}
	}
}
