package ui

import (
	"image"
	"image/color"
	"testing"
)

func newTestImage(w, h int) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

func TestFillRect(t *testing.T) {
	img := newTestImage(100, 100)
	r := image.Rect(10, 10, 50, 50)
	c := color.RGBA{R: 200, G: 100, B: 50, A: 255}
	FillRect(img, r, c)
	got := img.RGBAAt(30, 30)
	if got != c {
		t.Fatalf("expected %v at (30,30), got %v", c, got)
	}
	// Outside rect should be zero.
	zero := img.RGBAAt(5, 5)
	if zero.R != 0 {
		t.Fatalf("pixel outside rect should be zero, got %v", zero)
	}
}

func TestDrawText_NonZero(t *testing.T) {
	img := newTestImage(1280, 100)
	DrawText(img, 10, 10, "SimpleClient", ColorText)
	// At least one non-zero pixel should exist.
	found := false
	for x := 0; x < 100; x++ {
		for y := 0; y < 30; y++ {
			px := img.RGBAAt(x, y)
			if px.R != 0 || px.G != 0 || px.B != 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("DrawText produced no visible pixels")
	}
}

func TestDrawTextLarge_NonZero(t *testing.T) {
	img := newTestImage(1280, 100)
	DrawTextLarge(img, 10, 10, "SimpleClient", ColorAccent)
	found := false
	for x := 10; x < 150; x++ {
		for y := 10; y < 60; y++ {
			px := img.RGBAAt(x, y)
			if px.R != 0 || px.G != 0 || px.B != 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("DrawTextLarge produced no visible pixels")
	}
}

func TestDrawCursor_NonZero(t *testing.T) {
	img := newTestImage(100, 100)
	DrawCursor(img, 50, 50)
	// The cursor draws at the crosshair center.
	px := img.RGBAAt(50, 50)
	if px.R == 0 && px.G == 0 && px.B == 0 {
		t.Fatal("DrawCursor should produce visible pixels at cursor position")
	}
}

func TestTextWidth_Small(t *testing.T) {
	w := TextWidth("abc", false)
	if w != 3*CharW {
		t.Fatalf("TextWidth('abc', false) = %d, want %d", w, 3*CharW)
	}
}

func TestTextWidth_Large(t *testing.T) {
	w := TextWidth("ab", true)
	if w != 2*CharW*2 {
		t.Fatalf("TextWidth('ab', true) = %d, want %d", w, 2*CharW*2)
	}
}

// countBarCells reports how many of the meter's cells are lit. The meter is
// drawn as discrete cells with gaps between them, so a bar cannot be checked by
// sweeping every pixel on a row — the gaps are deliberately left untouched.
func countBarCells(t *testing.T, pct float64, fg, bg color.RGBA) (lit, total int) {
	t.Helper()
	img := newTestImage(200, 20)
	r := image.Rect(0, 0, 200, 20)
	DrawProgressBar(img, r, pct, fg, bg)

	for i := 0; i < ProgressCells; i++ {
		x := i * 200 / ProgressCells // the left edge of cell i, always painted
		switch img.RGBAAt(x, 10) {
		case fg:
			lit++
			total++
		case bg:
			total++
		default:
			t.Fatalf("pct=%v: cell at x=%d is neither fg nor bg", pct, x)
		}
	}
	return lit, total
}

func TestDrawProgressBar_Empty(t *testing.T) {
	fg := color.RGBA{R: 0, G: 128, B: 255, A: 255}
	bg := color.RGBA{R: 40, G: 40, B: 40, A: 255}
	lit, total := countBarCells(t, 0.0, fg, bg)
	if total == 0 {
		t.Fatal("the meter drew no cells at all")
	}
	if lit != 0 {
		t.Fatalf("at pct=0, %d of %d cells are lit, want none", lit, total)
	}
}

func TestDrawProgressBar_Full(t *testing.T) {
	fg := color.RGBA{R: 0, G: 255, B: 0, A: 255}
	bg := color.RGBA{R: 40, G: 40, B: 40, A: 255}
	lit, total := countBarCells(t, 1.0, fg, bg)
	if total == 0 {
		t.Fatal("the meter drew no cells at all")
	}
	if lit != total {
		t.Fatalf("at pct=1, %d of %d cells are lit, want all", lit, total)
	}
}

// The meter has to advance monotonically, or a scan appears to stall and then
// jump rather than to progress.
func TestDrawProgressBar_AdvancesMonotonically(t *testing.T) {
	fg := color.RGBA{R: 0, G: 255, B: 0, A: 255}
	bg := color.RGBA{R: 40, G: 40, B: 40, A: 255}

	prev := -1
	for _, pct := range []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1} {
		lit, _ := countBarCells(t, pct, fg, bg)
		if lit < prev {
			t.Fatalf("at pct=%v the meter fell back to %d cells from %d", pct, lit, prev)
		}
		prev = lit
	}
}

func TestDrawInputField_ActiveBorder(t *testing.T) {
	img := newTestImage(300, 30)
	// Draw inactive and active variants; the border at top-left should differ.
	FillRect(img, img.Bounds(), ColorBG)
	r := image.Rect(5, 5, 295, 25)
	DrawInputField(img, r, "hello", false)
	inactiveBorder := img.RGBAAt(5, 5)

	FillRect(img, img.Bounds(), ColorBG)
	DrawInputField(img, r, "hello", true)
	activeBorder := img.RGBAAt(5, 5)

	if inactiveBorder == activeBorder {
		t.Fatal("active and inactive input fields should have different border colors")
	}
}
