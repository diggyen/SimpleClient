package ui

import (
	"image"
	"testing"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

// A rune with no glyph is drawn as a gap, not as a fallback box. That is the
// right behaviour for a translated string, but it would silently eat a letter
// out of the wordmark, which is the one string that must never lose one.
func TestLogoWordmark_IsFullyCovered(t *testing.T) {
	if !PixelTextCovers(logoWordA + logoWordB) {
		t.Fatalf("the bitmap face is missing a glyph used by %q", logoWordA+logoWordB)
	}
}

// The sprite is indexed by row and column with no bounds arithmetic, so a short
// or long line would draw a ragged edge rather than fail.
func TestLogoSprite_IsSquare(t *testing.T) {
	if len(logoSprite) != logoSpriteSize {
		t.Fatalf("the sprite has %d rows, want %d", len(logoSprite), logoSpriteSize)
	}
	for i, line := range logoSprite {
		if len(line) != logoSpriteSize {
			t.Errorf("sprite row %d is %d columns wide, want %d", i, len(line), logoSpriteSize)
		}
	}
}

// Every character in the sprite has to resolve to a colour or to transparency.
// A typo would otherwise punch a hole in the mark that only shows up on screen.
func TestLogoSprite_UsesOnlyKnownColors(t *testing.T) {
	for row, line := range logoSprite {
		for col := 0; col < len(line); col++ {
			ch := line[col]
			if ch == '.' {
				continue
			}
			if _, ok := logoPalette(ch); !ok {
				t.Errorf("sprite (%d,%d) uses unknown character %q", col, row, ch)
			}
		}
	}
}

// PixelTextWidth is what centres the wordmark, so it has to match the ink the
// drawing code actually lays down — not the advance width including the
// trailing blank column.
func TestPixelTextWidth_MatchesInk(t *testing.T) {
	const s = "SIMPLECLIENT"
	for _, scale := range []int{1, 3, 4} {
		w := PixelTextWidth(s, scale)
		img := newTestImage(w+4*scale, PixelTextHeight(scale)+4*scale)
		DrawPixelText(img, 0, 0, s, scale, ColorText)

		maxX := -1
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if img.RGBAAt(x, y).A != 0 && x > maxX {
					maxX = x
				}
			}
		}
		if maxX != w-1 {
			t.Errorf("scale %d: ink ends at x=%d, PixelTextWidth reports %d", scale, maxX, w)
		}
	}
}

// The mark, the wordmark and the strap have to stay inside the block the layout
// reports, or the card is positioned against a boundary the header overruns.
func TestLayoutLogo_PartsStayInsideTheBlock(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, w := range []int{640, 800, 1024, 1280, 1920} {
			l := layoutLogo(w, 40)

			if left, right := l.Bounds.Min.X, w-l.Bounds.Max.X; left != right {
				t.Errorf("lang=%s w=%d: block not centred (%d left, %d right)", lang, w, left, right)
			}
			if l.Bounds.Dy() != logoHeight(w) {
				t.Errorf("lang=%s w=%d: block is %dpx tall, logoHeight reports %d",
					lang, w, l.Bounds.Dy(), logoHeight(w))
			}
			for name, part := range map[string]image.Rectangle{
				"mark": l.Mark, "wordmark": l.Word, "tagline": l.Tagline,
			} {
				if !part.In(l.Bounds) {
					t.Errorf("lang=%s w=%d: %s %v escapes the block %v", lang, w, name, part, l.Bounds)
				}
			}
			if l.Mark.Overlaps(l.Word) || l.Mark.Overlaps(l.Tagline) {
				t.Errorf("lang=%s w=%d: the mark overlaps the text beside it", lang, w)
			}
		}
	}
}
