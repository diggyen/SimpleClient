package ui

import (
	"image"
	"image/color"
)

// A hand-authored 5×7 bitmap face, used for the wordmark and for the small
// all-caps labels that head a column or a section.
//
// Go Mono cannot do this job. It is an outline face, so at logo size it renders
// as large body text with softened, hinted stems — legible, but plainly the
// same typeface as the host list set bigger. A bitmap face scaled by whole
// pixels keeps every edge square at any size, and that hard square edge is what
// makes a mark read as 8-bit rather than merely small.
const (
	pixelGlyphW = 5 // ink columns per glyph
	pixelGlyphH = 7 // ink rows per glyph
	pixelGlyphA = 6 // advance: the glyph plus one blank column
)

// pixelGlyphs maps a rune to its 7 rows of 5 columns. '#' is ink, anything else
// is transparent. Only the subset the UI actually sets is defined; DrawPixelText
// draws an undefined rune as a blank cell rather than a fallback box, so a
// missing glyph shows up as a gap during review instead of corrupting a word.
var pixelGlyphs = map[rune][pixelGlyphH]string{
	'A': {".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'B': {"####.", "#...#", "#...#", "####.", "#...#", "#...#", "####."},
	'C': {".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."},
	'D': {"####.", "#...#", "#...#", "#...#", "#...#", "#...#", "####."},
	'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
	'F': {"#####", "#....", "#....", "####.", "#....", "#....", "#...."},
	'G': {".###.", "#...#", "#....", "#.###", "#...#", "#...#", ".###."},
	'H': {"#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'I': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "#####"},
	'J': {"..###", "...#.", "...#.", "...#.", "...#.", "#..#.", ".##.."},
	'K': {"#...#", "#..#.", "#.#..", "##...", "#.#..", "#..#.", "#...#"},
	'L': {"#....", "#....", "#....", "#....", "#....", "#....", "#####"},
	'M': {"#...#", "##.##", "#.#.#", "#.#.#", "#...#", "#...#", "#...#"},
	'N': {"#...#", "##..#", "##..#", "#.#.#", "#..##", "#..##", "#...#"},
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'Q': {".###.", "#...#", "#...#", "#...#", "#.#.#", "#..#.", ".##.#"},
	'R': {"####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"},
	'S': {".###.", "#...#", "#....", ".###.", "....#", "#...#", ".###."},
	'T': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."},
	'U': {"#...#", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'V': {"#...#", "#...#", "#...#", "#...#", "#...#", ".#.#.", "..#.."},
	'W': {"#...#", "#...#", "#...#", "#.#.#", "#.#.#", "##.##", "#...#"},
	'X': {"#...#", "#...#", ".#.#.", "..#..", ".#.#.", "#...#", "#...#"},
	'Y': {"#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."},
	'Z': {"#####", "....#", "...#.", "..#..", ".#...", "#....", "#####"},

	'0': {".###.", "#...#", "#..##", "#.#.#", "##..#", "#...#", ".###."},
	'1': {"..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."},
	'2': {".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	'3': {"#####", "...#.", "..#..", "...#.", "....#", "#...#", ".###."},
	'4': {"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	'5': {"#####", "#....", "####.", "....#", "....#", "#...#", ".###."},
	'6': {"..##.", ".#...", "#....", "####.", "#...#", "#...#", ".###."},
	'7': {"#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."},
	'8': {".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."},
	'9': {".###.", "#...#", "#...#", ".####", "....#", "...#.", ".##.."},

	' ': {".....", ".....", ".....", ".....", ".....", ".....", "....."},
	'.': {".....", ".....", ".....", ".....", ".....", ".....", "..#.."},
	'-': {".....", ".....", ".....", ".###.", ".....", ".....", "....."},
	':': {".....", "..#..", ".....", ".....", ".....", "..#..", "....."},
	'/': {"....#", "....#", "...#.", "..#..", ".#...", "#....", "#...."},
}

// PixelTextWidth returns the drawn width of s at the given scale. The trailing
// blank column of the last glyph is not counted, so a centred string is
// actually centred rather than a scale-width off.
func PixelTextWidth(s string, scale int) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return (n*pixelGlyphA - 1) * scale
}

// PixelTextHeight returns the drawn height of any string at the given scale.
func PixelTextHeight(scale int) int { return pixelGlyphH * scale }

// PixelTextCovers reports whether every rune in s has a glyph. The wordmark is
// fixed text and always covered, but translated strings are not: callers that
// set one in this face have to be able to fall back, or a language that reaches
// outside the subset above loses letters with nothing on screen to say so.
func PixelTextCovers(s string) bool {
	for _, r := range s {
		if _, ok := pixelGlyphs[r]; !ok {
			return false
		}
	}
	return true
}

// DrawPixelText renders s in the bitmap face with its top-left at (x, y), each
// font pixel drawn as a scale×scale block.
func DrawPixelText(img *image.RGBA, x, y int, s string, scale int, c color.RGBA) {
	if scale < 1 {
		scale = 1
	}
	for i, r := range []rune(s) {
		glyph, ok := pixelGlyphs[r]
		if !ok {
			continue
		}
		gx := x + i*pixelGlyphA*scale
		for row := 0; row < pixelGlyphH; row++ {
			line := glyph[row]
			for col := 0; col < pixelGlyphW && col < len(line); col++ {
				if line[col] != '#' {
					continue
				}
				FillRect(img, image.Rect(
					gx+col*scale, y+row*scale,
					gx+(col+1)*scale, y+(row+1)*scale,
				), c)
			}
		}
	}
}

// DrawPixelTextShadowed draws s twice: once offset by one font pixel in shadow,
// then in fg. The offset is one *scaled* pixel, so the shadow stays proportional
// as the mark grows and never turns into a hairline.
func DrawPixelTextShadowed(img *image.RGBA, x, y int, s string, scale int, fg, shadow color.RGBA) {
	DrawPixelText(img, x+scale, y+scale, s, scale, shadow)
	DrawPixelText(img, x, y, s, scale, fg)
}

// TrackedWidth returns the width of text drawn in the UI face with extra
// letter-spacing.
func TrackedWidth(text string, tracking int) int {
	n := len([]rune(text))
	if n == 0 {
		return 0
	}
	return n*CharW + (n-1)*tracking
}

// DrawTextTracked draws text in the UI face with extra spacing between letters.
// Small all-caps labels — the tagline, the column headings — are unreadable set
// solid at 7px; opening them up is what makes them scan as labels rather than
// as a smudge.
func DrawTextTracked(img *image.RGBA, x, y int, text string, tracking int, c color.RGBA) {
	for i, r := range []rune(text) {
		DrawText(img, x+i*(CharW+tracking), y, string(r), c)
	}
}
