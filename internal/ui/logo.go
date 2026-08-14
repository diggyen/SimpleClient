package ui

import (
	"image"
	"image/color"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

// The mark: a display, bezelled, with a session pointer inside it. It is the
// one picture that says what this box is for — you are looking at a screen that
// is not the screen in front of you — and it survives being drawn 16 pixels
// wide, which a plug, a globe or a pair of monitors does not.
//
// Legend: '#' outline, '-' bezel highlight, '+' bezel shade, '*' screen,
// 'g' the glint off the glass, '@' the pointer. Anything else is transparent.
//
// The pointer is the fiddly part. An arrow that gains a column on every row
// draws a 45° hypotenuse, which at this size reads as a plain triangle; what
// makes it a mouse pointer is the notch under the head and the tail running off
// it. It also has to keep a clear pixel between itself and the bezel on every
// side — an arrow touching the frame reads as clipped, not as placed.
var logoSprite = [...]string{
	".##############.",
	".#------------#.",
	".#-*********g+#.",
	".#-**@*******+#.",
	".#-**@@******+#.",
	".#-**@@@*****+#.",
	".#-**@@@@****+#.",
	".#-**@@@@@***+#.",
	".#-**@@@@@@**+#.",
	".#-**@@@@****+#.",
	".#-**@*@@@***+#.",
	".#-**********+#.",
	".#++++++++++++#.",
	".##############.",
	"......####......",
	"...##########...",
}

// logoSpriteSize is the sprite's side in font pixels. It is square, which is
// what lets the header centre it against the wordmark without a fudge factor.
const logoSpriteSize = 16

// logoWordmark is set in the bitmap face and split in two colours rather than
// in two cases: "SimpleClient" camel-cased would need a lowercase bitmap
// alphabet with descenders, and all-caps two-tone reads as the same compound
// word at a quarter of the glyph budget.
const (
	logoWordA = "SIMPLE"
	logoWordB = "CLIENT"
)

// logoScaleFor picks the block size for a screen width. 4× is the design size;
// 640×480 kiosks get 3×, where the block still clears the screen edge by more
// than the card does.
func logoScaleFor(screenW int) int {
	if screenW < 900 {
		return 3
	}
	return 4
}

// taglineTracking opens up the small all-caps strap under the wordmark.
const taglineTracking = 2

// logoLayout places the parts of the header block. Everything is absolute, so
// drawing and measuring cannot disagree about where the block ends.
type logoLayout struct {
	Bounds  image.Rectangle // the whole block
	Mark    image.Rectangle // the sprite
	Word    image.Rectangle // the wordmark
	Tagline image.Rectangle // the strap under the wordmark
	Scale   int
}

// layoutLogo centres the header block on a screen of width screenW, with the
// top of the block at y.
func layoutLogo(screenW, y int) logoLayout {
	scale := logoScaleFor(screenW)

	markSide := logoSpriteSize * scale
	gap := 5 * scale

	wordW := PixelTextWidth(logoWordA+logoWordB, scale)
	wordH := PixelTextHeight(scale)

	tagline := i18n.T(i18n.Tagline)
	tagW := TrackedWidth(tagline, taglineTracking)

	textW := wordW
	if tagW > textW {
		textW = tagW
	}

	// The wordmark and its strap are centred as a unit against the mark, so the
	// optical centre of the block sits on the mark's centre line whatever the
	// active language does to the strap's width.
	const strapGap = 9
	textH := wordH + strapGap + CharH

	totalW := markSide + gap + textW
	x := (screenW - totalW) / 2

	textY := y + (markSide-textH)/2
	textX := x + markSide + gap

	return logoLayout{
		Bounds:  image.Rect(x, y, x+totalW, y+markSide),
		Mark:    image.Rect(x, y, x+markSide, y+markSide),
		Word:    image.Rect(textX, textY, textX+wordW, textY+wordH),
		Tagline: image.Rect(textX, textY+wordH+strapGap, textX+tagW, textY+wordH+strapGap+CharH),
		Scale:   scale,
	}
}

// logoHeight is the vertical space layoutLogo will occupy on a screenW screen.
func logoHeight(screenW int) int { return logoSpriteSize * logoScaleFor(screenW) }

// drawLogo paints the header block: mark, two-tone wordmark, strap.
func drawLogo(img *image.RGBA, l logoLayout) {
	drawLogoMark(img, l.Mark.Min.X, l.Mark.Min.Y, l.Scale)

	DrawPixelTextShadowed(img, l.Word.Min.X, l.Word.Min.Y, logoWordA, l.Scale,
		ColorText, ColorLogoShadow)
	// +Scale restores the single blank column PixelTextWidth trims from the end
	// of the first half, so the two halves keep the same rhythm as one word.
	DrawPixelTextShadowed(img,
		l.Word.Min.X+PixelTextWidth(logoWordA, l.Scale)+l.Scale,
		l.Word.Min.Y, logoWordB, l.Scale,
		ColorAccent, ColorLogoShadow)

	DrawTextTracked(img, l.Tagline.Min.X, l.Tagline.Min.Y,
		i18n.T(i18n.Tagline), taglineTracking, ColorDim)
}

// drawLogoMark blits the sprite with its top-left at (x, y), one sprite pixel
// per scale×scale block.
func drawLogoMark(img *image.RGBA, x, y, scale int) {
	if scale < 1 {
		scale = 1
	}
	for row, line := range logoSprite {
		for col := 0; col < len(line); col++ {
			c, ok := logoPalette(line[col])
			if !ok {
				continue
			}
			FillRect(img, image.Rect(
				x+col*scale, y+row*scale,
				x+(col+1)*scale, y+(row+1)*scale,
			), c)
		}
	}
}

// logoPalette resolves one sprite character to its colour.
func logoPalette(ch byte) (color.RGBA, bool) {
	switch ch {
	case '#':
		return ColorLogoOutline, true
	case '-':
		return ColorLogoBezelHi, true
	case '+':
		return ColorLogoBezelLo, true
	case '*':
		return ColorLogoScreen, true
	case 'g':
		return ColorLogoGlint, true
	case '@':
		return ColorAccent, true
	}
	return color.RGBA{}, false
}
