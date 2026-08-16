package ui

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// CharW and CharH are the pixel dimensions of one character cell.
// The whole layout is built on this grid, so the faces below are sized to match
// it exactly rather than the other way round.
const (
	CharW = 7
	CharH = 13
)

// Font sizes chosen so Go Mono's advance width lands on exact pixel multiples of
// CharW at 72 DPI: 11pt → 7px advance, 23pt → 14px advance. Anything in between
// rounds to a different width and breaks the column grid.
const (
	fontSizeSmall = 11
	fontSizeLarge = 23
)

// Baselines derived from the faces' ascent, so that drawing at y puts the glyph
// tops at y (matching the rest of the layout, which positions by top-left).
var baselineSmall, baselineLarge int

// FontFace7x13 is the small UI face: Go Mono at a 7×13 cell.
//
// Go Mono replaces basicfont.Face7x13 (ASCII-only) because the UI is localised:
// Turkish needs ğ ı İ ş ç ö ü, and the discovery screen uses ↑ ↓ ▲ ▼. Both are
// covered by Go Mono, which ships inside golang.org/x/image — no new dependency
// and no font file to vendor.
var FontFace7x13 font.Face

// fontFaceLarge is the 2× face used for the title and the empty-list message.
var fontFaceLarge font.Face

func init() {
	f, err := sfnt.Parse(gomono.TTF)
	if err != nil {
		// gomono.TTF is a compile-time constant asset; a parse failure means the
		// binary itself is corrupt and there is nothing sensible to fall back to.
		panic("ui: parsing embedded Go Mono font: " + err.Error())
	}

	FontFace7x13 = mustFace(f, fontSizeSmall)
	fontFaceLarge = mustFace(f, fontSizeLarge)

	baselineSmall = FontFace7x13.Metrics().Ascent.Ceil()
	baselineLarge = fontFaceLarge.Metrics().Ascent.Ceil()
}

func mustFace(f *sfnt.Font, size float64) font.Face {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull, // crisp stems at these small sizes
	})
	if err != nil {
		panic("ui: building font face: " + err.Error())
	}
	return face
}

// TextWidth returns the pixel width of text rendered at the given scale.
// large=true doubles each character.
func TextWidth(text string, large bool) int {
	w := len([]rune(text)) * CharW
	if large {
		return w * 2
	}
	return w
}

// DrawText renders text with its top-left corner at (x, y).
func DrawText(img *image.RGBA, x, y int, text string, c color.RGBA) {
	drawWithFace(img, x, y, text, c, FontFace7x13, baselineSmall)
}

// DrawTextLarge renders text at 2× scale (14×26 per character cell).
func DrawTextLarge(img *image.RGBA, x, y int, text string, c color.RGBA) {
	drawWithFace(img, x, y, text, c, fontFaceLarge, baselineLarge)
}

func drawWithFace(img *image.RGBA, x, y int, text string, c color.RGBA, face font.Face, baseline int) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, y+baseline),
	}
	d.DrawString(text)
}
