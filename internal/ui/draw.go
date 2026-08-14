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
	DrawHLine(img, r.Min.X, r.Max.X, r.Min.Y, c)   // top
	DrawHLine(img, r.Min.X, r.Max.X, r.Max.Y-1, c) // bottom
	DrawVLine(img, r.Min.X, r.Min.Y, r.Max.Y, c)   // left
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

// cornerInset is the horizontal inset applied to the first rows of a notched
// rectangle, measured from the nearest horizontal edge. Two steps read as a
// rounded corner at pixel scale; a real radius would need anti-aliasing, which
// this renderer does not have and which would fight the 8-bit mark anyway.
var cornerInset = [...]int{2, 1}

// rowInset returns how far row y is pulled in from the sides of r.
func rowInset(r image.Rectangle, y int) int {
	d := y - r.Min.Y
	if fromBottom := r.Max.Y - 1 - y; fromBottom < d {
		d = fromBottom
	}
	if d < 0 || d >= len(cornerInset) {
		return 0
	}
	return cornerInset[d]
}

// FillNotched fills r with its corners stepped in, so panels read as chunky
// pixel-art rectangles rather than as plain boxes.
func FillNotched(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		in := rowInset(r, y)
		FillRect(img, image.Rect(r.Min.X+in, y, r.Max.X-in, y+1), c)
	}
}

// FillNotchedTop notches only the top corners. Strips that butt onto the rest
// of a panel — a card's title bar — need a square bottom edge, or two pixels of
// the surface below show through at each end and read as a rendering fault.
func FillNotchedTop(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		in := 0
		if d := y - r.Min.Y; d < len(cornerInset) {
			in = cornerInset[d]
		}
		FillRect(img, image.Rect(r.Min.X+in, y, r.Max.X-in, y+1), c)
	}
}

// FillNotchedBlend is FillNotched with alpha, used for the drop shadows so they
// carry the same silhouette as the panel above them.
func FillNotchedBlend(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		in := rowInset(r, y)
		FillRectBlend(img, image.Rect(r.Min.X+in, y, r.Max.X-in, y+1), c)
	}
}

// DrawPanel draws a notched panel: a 1px border, a fill, and a lit top edge.
// The border is painted as a full notched rectangle and then overdrawn one
// pixel in from every side, which gets the stepped corners right for free.
func DrawPanel(img *image.RGBA, r image.Rectangle, fill, border color.RGBA) {
	FillNotchedBlend(img, r.Add(image.Pt(5, 6)), ColorCardShadow)
	FillNotched(img, r, border)
	FillNotched(img, r.Inset(1), fill)

	// A single lit row along the top. Light comes from above on every other
	// surface in this UI, and without it a flat fill inside a flat border reads
	// as a hole rather than as a raised card.
	DrawHLine(img, r.Min.X+2, r.Max.X-2, r.Min.Y+1, ColorPanelEdge)
}

// DrawProgressBar renders a segmented progress meter inside rect r.
// pct should be in [0.0, 1.0].
//
// The meter is drawn as discrete cells rather than as a sliding fill: a solid
// bar creeping forward by a pixel a second is hard to read as motion, whereas a
// cell lighting up is unmistakable, and the cells match the mark.
func DrawProgressBar(img *image.RGBA, r image.Rectangle, pct float64, fg, bg color.RGBA) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}

	// A fixed cell count rather than a fixed cell width: derived from the width,
	// a 1280px card ends up with over a hundred cells, which is a dashed line
	// and not a meter. At 32 the cells stay chunky at every card size.
	lit := int(float64(ProgressCells)*pct + 0.5)
	for i := 0; i < ProgressCells; i++ {
		x := r.Min.X + i*r.Dx()/ProgressCells
		end := r.Min.X + (i+1)*r.Dx()/ProgressCells - progressCellGap
		if end <= x {
			end = x + 1
		}
		cell := image.Rect(x, r.Min.Y, end, r.Max.Y)
		if i < lit {
			FillRect(img, cell, fg)
		} else {
			FillRect(img, cell, bg)
		}
	}
}

// ProgressCells is how many cells DrawProgressBar divides its rectangle into.
const ProgressCells = 32

// progressCellGap is the unpainted gutter at the right of every cell.
const progressCellGap = 3

// SignalBars is the number of steps in the staircase DrawSignal draws.
const SignalBars = 4

// SignalWidth is the drawn width of a signal staircase.
const SignalWidth = SignalBars*4 - 1 // 3px bars with a 1px gap between them

// DrawSignal draws a rising staircase of bars with the first `strength` of them
// lit, bottom-aligned to r.Max.Y.
//
// It shows link quality the way every phone and laptop in the building already
// does — more bars is better — which is the opposite of showing latency as a
// bar that grows as the link gets worse. A "long red bar" reading as *good* is
// exactly the misreading that matters here, because it is the row the operator
// is about to connect to.
func DrawSignal(img *image.RGBA, r image.Rectangle, strength int, fg color.RGBA) {
	for i := 0; i < SignalBars; i++ {
		h := r.Dy() * (i + 2) / (SignalBars + 1)
		x := r.Min.X + i*4
		bar := image.Rect(x, r.Max.Y-h, x+3, r.Max.Y)

		if i < strength {
			FillRect(img, bar, fg)
		} else {
			FillRect(img, bar, ColorTrack)
		}
	}
}

// cursorSprite is the mouse pointer, drawn from the same 1-bit vocabulary as
// the logo: '#' outline, ' ' body, '.' transparent.
var cursorSprite = [...]string{
	"#...........",
	"##..........",
	"# #.........",
	"#  #........",
	"#   #.......",
	"#    #......",
	"#     #.....",
	"#      #....",
	"#       #...",
	"#     ####..",
	"#  ## #.....",
	"# #  # #....",
	"##   # #....",
	"#.....# #...",
	"......# #...",
	".......##...",
}

// DrawCursor draws the mouse pointer with its hotspot at (x, y).
//
// It replaced a crosshair, which was indistinguishable from the UI's own
// hairlines wherever it crossed a border, and which gave no hint of where the
// hotspot actually was.
func DrawCursor(img *image.RGBA, x, y int) {
	for row, line := range cursorSprite {
		for col := 0; col < len(line); col++ {
			switch line[col] {
			case '#':
				BlendPixel(img, x+col, y+row, ColorLogoOutline)
			case ' ':
				BlendPixel(img, x+col, y+row, ColorCursor)
			}
		}
	}
}

// spinnerRing walks the eight cells of a 3×3 grid, clockwise from the top-left,
// as (column, row) pairs. The centre is left empty.
var spinnerRing = [8][2]int{{0, 0}, {1, 0}, {2, 0}, {2, 1}, {2, 2}, {1, 2}, {0, 2}, {0, 1}}

// SpinnerSize is the side of the square DrawSpinner draws, for a given cell.
func SpinnerSize(cell int) int { return 3*cell + 2*spinnerGap }

const spinnerGap = 3

// DrawSpinner draws a block chasing round a ring, with its top-left at (x, y).
//
// This replaced a spinning `|/-\`, which was a single 7px glyph of the same
// typeface as the text under it: at a glance it looked like a stray character
// rather than like something moving.
func DrawSpinner(img *image.RGBA, x, y, tick, cell int, fg color.RGBA) {
	trail := [...]color.RGBA{fg, ColorAccentDim, ColorAccentFaint}
	head := tick % len(spinnerRing)

	for i, p := range spinnerRing {
		c := ColorTrack
		if dist := (head - i + len(spinnerRing)) % len(spinnerRing); dist < len(trail) {
			c = trail[dist]
		}
		cx := x + p[0]*(cell+spinnerGap)
		cy := y + p[1]*(cell+spinnerGap)
		FillRect(img, image.Rect(cx, cy, cx+cell, cy+cell), c)
	}
}

// DrawChevron draws the small solid triangle that marks the selected row. It is
// drawn rather than set as a glyph because Go Mono's coverage of the geometric
// block is patchy, and a marker that silently renders as nothing is worse than
// no marker at all.
func DrawChevron(img *image.RGBA, x, y int, c color.RGBA) {
	const h = 7 // odd, so the triangle has a single-pixel tip
	for row := 0; row < h; row++ {
		w := row + 1
		if row >= h/2 {
			w = h - row
		}
		FillRect(img, image.Rect(x, y+row, x+w, y+row+1), c)
	}
}

// DrawKeycap draws a key legend as a small raised cap and returns its width, so
// a row of hints can be laid out by accumulation.
func DrawKeycap(img *image.RGBA, x, y int, label string) int {
	w := TextWidth(label, false) + 10
	h := CharH + 8
	r := image.Rect(x, y, x+w, y+h)

	FillNotched(img, r, ColorBorder)
	FillNotched(img, r.Inset(1), ColorPanel)
	DrawHLine(img, r.Min.X+2, r.Max.X-2, r.Min.Y+1, ColorPanelEdge)
	DrawText(img, x+5, y+4, label, ColorMuted)
	return w
}

// DrawInputField renders a text input field. The active field gets an accent border.
func DrawInputField(img *image.RGBA, r image.Rectangle, text string, active bool) {
	// Background
	FillRect(img, r, ColorBG)

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
