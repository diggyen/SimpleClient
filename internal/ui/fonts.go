package ui

import (
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
)

// FontFace7x13 is the standard 7×13 bitmap font (one rune = 7px wide, 13px tall).
var FontFace7x13 font.Face = basicfont.Face7x13

// CharW and CharH are the pixel dimensions of one character in the basic font.
const (
	CharW = 7
	CharH = 13
)

// TextWidth returns the pixel width of text rendered at the given scale.
// large=true doubles each character.
func TextWidth(text string, large bool) int {
	w := len([]rune(text)) * CharW
	if large {
		return w * 2
	}
	return w
}

// DrawText renders text at (x, y) using the 7×13 basicfont.
func DrawText(img *image.RGBA, x, y int, text string, c color.RGBA) {
	dot := fixed.P(x, y+CharH-2) // baseline adjustment
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: FontFace7x13,
		Dot:  dot,
	}
	d.DrawString(text)
}

// DrawTextLarge renders text at 2× scale (14×26 per character).
func DrawTextLarge(img *image.RGBA, x, y int, text string, c color.RGBA) {
	// Render at normal size into a small buffer, then scale 2×.
	runes := []rune(text)
	w := len(runes) * CharW
	h := CharH
	tmp := image.NewRGBA(image.Rect(0, 0, w, h))
	DrawText(tmp, 0, 0, text, c)

	// Scale 2× using nearest-neighbour.
	for sy := 0; sy < h; sy++ {
		for sx := 0; sx < w; sx++ {
			px := tmp.RGBAAt(sx, sy)
			if px.A == 0 {
				continue
			}
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					img.SetRGBA(x+sx*2+dx, y+sy*2+dy, px)
				}
			}
		}
	}
}

// FillUniform fills img with a uniform color (used as background clearing helper).
func FillUniform(img *image.RGBA, c color.RGBA) {
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
}
