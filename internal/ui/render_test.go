package ui

import (
	"bytes"
	"image"
	"net"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
)

func TestRenderDiscovery_Empty(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{Screen: ScreenDiscovery}
	Render(fb, back, state, 640, 360)

	// Top bar and bottom bar should be non-background-colored pixels.
	topPx := back.RGBAAt(10, 10)
	if topPx == ColorBG {
		t.Fatal("top bar should not be same as background color")
	}
}

func TestRenderDiscovery_ThreeHosts(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{
		Screen: ScreenDiscovery,
		Hosts: []domain.Host{
			{IP: net.ParseIP("192.168.1.1"), Hostname: "server1", DiscoveredAt: time.Now()},
			{IP: net.ParseIP("192.168.1.2"), Hostname: "server2", DiscoveredAt: time.Now()},
			{IP: net.ParseIP("192.168.1.3"), Hostname: "server3", DiscoveredAt: time.Now()},
		},
	}
	Render(fb, back, state, 0, 0)

	// The list area should have non-bg pixels (host rows rendered).
	listY := barH + 10
	hasContent := false
	for x := 10; x < 300; x++ {
		if back.RGBAAt(x, listY) != ColorBG {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatal("discovery screen with 3 hosts should have content in list area")
	}
}

func TestRenderDiscovery_SelectedRow(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{
		Screen:      ScreenDiscovery,
		SelectedIdx: 0,
		Hosts: []domain.Host{
			{IP: net.ParseIP("10.0.0.1"), DiscoveredAt: time.Now()},
		},
	}
	renderDiscovery(back, state)

	// Row 0 at listTop+4 should be highlighted (ColorSelected or border).
	rowY := barH + 2 + 4
	rowPx := back.RGBAAt(10, rowY)
	if rowPx == ColorBG {
		t.Fatal("selected row should not be plain background color")
	}
}

func TestRenderModal_NonEmpty(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{Screen: ScreenModal}
	renderDiscovery(back, state)
	renderModal(back, state)

	// Centre of modal should not be background.
	cx, cy := 640, 360
	px := back.RGBAAt(cx, cy)
	if px == ColorBG {
		t.Fatal("modal centre should not be plain background color")
	}
}

// --- Localisation / glyph coverage ---

// countInk reports how many non-transparent pixels img contains.
func countInk(img *image.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A != 0 {
				n++
			}
		}
	}
	return n
}

// The UI font was switched from basicfont.Face7x13 (ASCII-only) to Go Mono
// precisely so the Turkish locale renders. Every one of these glyphs used to
// come out blank.
func TestDrawText_TurkishGlyphsRender(t *testing.T) {
	for _, r := range []rune{'ç', 'Ç', 'ğ', 'Ğ', 'ı', 'İ', 'ö', 'Ö', 'ş', 'Ş', 'ü', 'Ü'} {
		img := newTestImage(4*CharW, 4*CharH)
		DrawText(img, 0, 0, string(r), ColorText)
		if countInk(img) == 0 {
			t.Errorf("glyph %q rendered no pixels", r)
		}
	}
}

// The discovery screen's scroll arrows are non-ASCII too.
func TestDrawText_ScrollArrowsRender(t *testing.T) {
	for _, r := range []rune{'▲', '▼', '↑', '↓'} {
		img := newTestImage(4*CharW, 4*CharH)
		DrawText(img, 0, 0, string(r), ColorText)
		if countInk(img) == 0 {
			t.Errorf("glyph %q rendered no pixels", r)
		}
	}
}

// Every spinner frame must be drawable — the previous braille frames were not.
func TestSpinnerFrames_Render(t *testing.T) {
	for _, f := range spinnerFrames {
		img := newTestImage(4*CharW, 4*CharH)
		DrawText(img, 0, 0, f, ColorText)
		if countInk(img) == 0 {
			t.Errorf("spinner frame %q rendered no pixels", f)
		}
	}
}

// Text must stay inside the cell grid the layout is built on: exactly CharW per
// rune, glyph tops at the requested y.
func TestDrawText_FitsCellGrid(t *testing.T) {
	const s = "Wg"
	img := newTestImage(10*CharW, 4*CharH)
	DrawText(img, 0, 0, s, ColorText)

	b := img.Bounds()
	maxX, maxY := -1, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A != 0 {
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX >= TextWidth(s, false) {
		t.Errorf("text overflows its advance width: ink at x=%d, width=%d", maxX, TextWidth(s, false))
	}
	if maxY >= CharH+2 { // +2 tolerance for the descender of 'g'
		t.Errorf("text overflows its cell height: ink at y=%d, CharH=%d", maxY, CharH)
	}
}

// Switching language must change what the discovery screen draws.
func TestRenderDiscovery_FollowsLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	render := func() *image.RGBA {
		fb := framebuffer.NewMock(1280, 720)
		back := image.NewRGBA(fb.Bounds())
		renderDiscovery(back, &UIState{Screen: ScreenDiscovery, ScanDone: true})
		return back
	}

	i18n.Set(i18n.LangEN)
	en := render()
	i18n.Set(i18n.LangTR)
	tr := render()

	if bytes.Equal(en.Pix, tr.Pix) {
		t.Fatal("discovery screen renders identically in English and Turkish")
	}
}

// The bottom bar used to draw the project URL underneath the progress bar.
// The URL is now dropped whenever it would collide, at either language width.
func TestRenderDiscovery_BottomBarNoOverlap(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, w := range []int{800, 1024, 1280, 1920} {
			fb := framebuffer.NewMock(w, 720)
			back := image.NewRGBA(fb.Bounds())
			renderDiscovery(back, &UIState{Screen: ScreenDiscovery})

			hintsEnd := padding + TextWidth(i18n.T(i18n.KeyHints), false)
			pbStart := w - progressBarW - padding
			if hintsEnd >= pbStart {
				t.Errorf("lang=%s w=%d: key hints (end %d) run into the progress bar (start %d)",
					lang, w, hintsEnd, pbStart)
			}
		}
	}
}
