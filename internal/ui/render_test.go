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
	"github.com/diggyen/SimpleClient/internal/version"
)

func TestRenderDiscovery_Empty(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{Screen: ScreenDiscovery}
	Render(fb, back, state, 640, 360)

	// The host list lives on a centred card, not an edge-to-edge bar.
	l := layoutDiscoveryFor(fb.Bounds(), state.Hosts)
	// Sample the header strip between the title and the count, clear of text.
	if got := back.RGBAAt(l.Card.Min.X+l.Card.Dx()/2, l.Card.Min.Y+4); got != ColorCardHeader {
		t.Fatalf("card header = %v, want ColorCardHeader", got)
	}
	if got := back.RGBAAt(10, 10); got != ColorBG {
		t.Fatalf("screen margin = %v, want plain background around the card", got)
	}
}

// The card must stay centred and inside the screen at every resolution the
// kiosk is likely to boot at.
func TestLayoutDiscovery_CardIsCentredAndFits(t *testing.T) {
	sizes := [][2]int{{800, 600}, {1024, 768}, {1280, 800}, {1920, 1080}, {640, 480}}
	for _, s := range sizes {
		screen := image.Rect(0, 0, s[0], s[1])
		l := layoutDiscovery(screen)

		if !l.Card.In(screen) {
			t.Errorf("%dx%d: card %v is not inside the screen", s[0], s[1], l.Card)
		}
		if left, right := l.Card.Min.X, screen.Max.X-l.Card.Max.X; left != right {
			t.Errorf("%dx%d: card not horizontally centred (%d left, %d right)", s[0], s[1], left, right)
		}
		if l.Rows < 1 {
			t.Errorf("%dx%d: card has room for %d rows", s[0], s[1], l.Rows)
		}
		if !l.List.In(l.Card) {
			t.Errorf("%dx%d: list %v escapes the card %v", s[0], s[1], l.List, l.Card)
		}
	}
}

// Every row the layout reports must map back to itself when clicked, otherwise
// a click selects a different host from the one under the pointer.
func TestLayoutDiscovery_RowHitTestRoundTrips(t *testing.T) {
	l := layoutDiscovery(image.Rect(0, 0, 1280, 800))
	for i := 0; i < l.Rows; i++ {
		r := l.rowRect(i)
		mid := r.Min.Y + r.Dy()/2
		if got := l.rowAt(r.Min.X+5, mid); got != i {
			t.Errorf("click in row %d resolved to row %d", i, got)
		}
	}
	if got := l.rowAt(l.List.Min.X+5, l.List.Min.Y-5); got != -1 {
		t.Errorf("click above the list resolved to row %d", got)
	}
	if got := l.rowAt(l.List.Min.X+5, l.List.Max.Y+5); got != -1 {
		t.Errorf("click below the list resolved to row %d", got)
	}
	if got := l.rowAt(l.Card.Min.X-5, l.List.Min.Y+5); got != -1 {
		t.Errorf("click left of the card resolved to row %d", got)
	}
}

// Latency is graded by colour so a list can be scanned without reading numbers.
func TestLatencyColor(t *testing.T) {
	if LatencyColor(0) != ColorMuted {
		t.Error("an unmeasured latency should be muted")
	}
	if LatencyColor(10) != ColorSuccess {
		t.Error("a fast host should be green")
	}
	if LatencyColor(100) != ColorWarning {
		t.Error("a middling host should be amber")
	}
	if LatencyColor(400) != ColorError {
		t.Error("a slow host should be red")
	}
}

func TestWrapText(t *testing.T) {
	got := wrapText("one two three four five", 9)
	want := []string{"one two", "three", "four five"}
	if len(got) != len(want) {
		t.Fatalf("wrapText produced %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wrapText produced %q, want %q", got, want)
		}
	}
	for _, line := range got {
		if len([]rune(line)) > 9 {
			t.Errorf("line %q exceeds the width limit", line)
		}
	}
	if wrapText("anything", 0) != nil {
		t.Error("a non-positive width should produce no lines")
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
	l := layoutDiscoveryFor(fb.Bounds(), state.Hosts)
	row := l.rowRect(0)
	hasContent := false
	for x := row.Min.X; x < row.Max.X; x++ {
		if back.RGBAAt(x, row.Min.Y+row.Dy()/2) != ColorBG {
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

	// The selected row carries the accent bar on its leading edge.
	l := layoutDiscoveryFor(fb.Bounds(), state.Hosts)
	row := l.rowRect(0)
	if got := back.RGBAAt(row.Min.X, row.Min.Y+row.Dy()/2); got != ColorAccent {
		t.Fatalf("selected row leading edge = %v, want the accent bar", got)
	}
	if got := back.RGBAAt(row.Min.X+20, row.Min.Y+1); got != ColorSelected {
		t.Fatalf("selected row background = %v, want ColorSelected", got)
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

// The spinner is drawn, not set as a glyph, so what it has to guarantee is that
// the head actually moves: a loader stuck on one cell says "hung", which is the
// opposite of what it is there to say.
func TestDrawSpinner_HeadMoves(t *testing.T) {
	const cell = 9
	size := SpinnerSize(cell)

	seen := map[string]bool{}
	for tick := 0; tick < len(spinnerRing); tick++ {
		img := newTestImage(size, size)
		DrawSpinner(img, 0, 0, tick, cell, ColorAccent)

		head := ""
		for _, p := range spinnerRing {
			x := p[0]*(cell+spinnerGap) + cell/2
			y := p[1]*(cell+spinnerGap) + cell/2
			if img.RGBAAt(x, y) == ColorAccent {
				head = string(rune('a' + p[0] + 3*p[1]))
			}
		}
		if head == "" {
			t.Fatalf("tick %d lit no head cell", tick)
		}
		if seen[head] {
			t.Fatalf("tick %d put the head back on a cell it has already used", tick)
		}
		seen[head] = true
	}
	if len(seen) != len(spinnerRing) {
		t.Fatalf("the head visited %d of %d cells in a full cycle", len(seen), len(spinnerRing))
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

// The footer legend and the header block are both built from translated
// strings, so either can outgrow the screen when the language changes. Both
// must still fit at the narrowest resolution the kiosk boots at.
func TestRenderDiscovery_HeaderAndHintsFitEveryLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, w := range []int{640, 800, 1024, 1280, 1920} {
			fb := framebuffer.NewMock(w, 720)
			back := image.NewRGBA(fb.Bounds())
			renderDiscovery(back, &UIState{Screen: ScreenDiscovery})

			if hintsW := keyHintsWidth(); hintsW > w-2*padding {
				t.Errorf("lang=%s w=%d: key hints (%dpx) do not fit on screen", lang, w, hintsW)
			}

			logo := layoutLogo(w, logoTop)
			if logo.Bounds.Min.X < padding || logo.Bounds.Max.X > w-padding {
				t.Errorf("lang=%s w=%d: logo block %v runs off screen", lang, w, logo.Bounds)
			}
			if logo.Tagline.Max.X > w-padding {
				t.Errorf("lang=%s w=%d: tagline runs off screen at x=%d", lang, w, logo.Tagline.Max.X)
			}
		}
	}
}

// The card grows with the list instead of leaving a mostly empty panel, but
// never beyond what fits — and once it is full the layout must match the
// fixed-size one Run uses for scrolling, or clicks and selection drift apart.
func TestLayoutDiscovery_CardGrowsWithTheList(t *testing.T) {
	screen := image.Rect(0, 0, 1280, 800)
	full := layoutDiscovery(screen)

	short := layoutDiscoveryFor(screen, sampleHosts(3))
	if short.Rows >= full.Rows {
		t.Errorf("a 3-host card shows %d rows, the full card %d — it should be smaller",
			short.Rows, full.Rows)
	}
	if short.Card.Dy() >= full.Card.Dy() {
		t.Error("a short list should produce a shorter card")
	}

	long := layoutDiscoveryFor(screen, sampleHosts(500))
	if long != full {
		t.Error("a list longer than the screen must use the same layout as Run's")
	}
}

// --- Credential dialog ---

// The dialog is centred and its buttons hit-test where they are drawn.
func TestLayoutModal_CentredAndHitTestable(t *testing.T) {
	for _, s := range [][2]int{{800, 600}, {1024, 768}, {1280, 800}} {
		screen := image.Rect(0, 0, s[0], s[1])
		l := layoutModal(screen)

		if !l.Box.In(screen) {
			t.Errorf("%dx%d: dialog %v is not inside the screen", s[0], s[1], l.Box)
		}
		if left, right := l.Box.Min.X, screen.Max.X-l.Box.Max.X; left != right {
			t.Errorf("%dx%d: dialog not horizontally centred", s[0], s[1])
		}
		if l.Connect.Overlaps(l.Cancel) {
			t.Errorf("%dx%d: the buttons overlap", s[0], s[1])
		}
		for i, f := range l.Fields {
			if !f.In(l.Box) {
				t.Errorf("%dx%d: field %d escapes the dialog", s[0], s[1], i)
			}
			if f.Overlaps(l.Connect) || f.Overlaps(l.Cancel) {
				t.Errorf("%dx%d: field %d overlaps the buttons", s[0], s[1], i)
			}
		}
	}
}

// Clicking a field focuses it; clicking Cancel closes the dialog.
func TestModalClick_FocusesFieldAndCancels(t *testing.T) {
	fb := framebuffer.NewMock(1280, 800)
	l := layoutModal(fb.Bounds())

	state := &UIState{Screen: ScreenModal}
	pwd := l.Fields[1]
	handleMouseClick(pwd.Min.X+10, pwd.Min.Y+5, state, fb, nil)
	if state.Modal.FocusIdx != 1 {
		t.Fatalf("clicking the password field set focus to %d, want 1", state.Modal.FocusIdx)
	}

	handleMouseClick(l.Cancel.Min.X+5, l.Cancel.Min.Y+5, state, fb, nil)
	if state.Screen != ScreenDiscovery {
		t.Fatal("clicking Cancel should return to the host list")
	}
}

// The dialog names the host it is about to connect to.
func TestRenderModal_ShowsSelectedHost(t *testing.T) {
	fb := framebuffer.NewMock(1280, 800)
	withHost := image.NewRGBA(fb.Bounds())
	withoutHost := image.NewRGBA(fb.Bounds())

	renderModal(withHost, &UIState{
		Screen: ScreenModal,
		Hosts:  []domain.Host{{IP: net.ParseIP("10.0.0.9"), Hostname: "veeam-2"}},
	})
	renderModal(withoutHost, &UIState{Screen: ScreenModal})

	if bytes.Equal(withHost.Pix, withoutHost.Pix) {
		t.Fatal("the dialog should name the selected host in its header")
	}
}

// The footer separator is non-ASCII. Go Mono covers it, but the braille spinner
// frames this project once shipped were also "obviously fine" and rendered as
// nothing at all.
func TestDrawText_FooterSeparatorRenders(t *testing.T) {
	img := newTestImage(8*CharW, 4*CharH)
	DrawText(img, 0, 0, buildSep, ColorText)
	if countInk(img) == 0 {
		t.Fatalf("separator %q rendered no pixels", buildSep)
	}
}

// The kiosk has no shell and no other way to be asked what it is, so the build
// has to be readable off the screen — the issue template asks reporters for it.
func TestRenderDiscovery_ShowsTheVersion(t *testing.T) {
	orig := version.Version
	t.Cleanup(func() { version.Version = orig })

	render := func() *image.RGBA {
		fb := framebuffer.NewMock(1280, 800)
		back := image.NewRGBA(fb.Bounds())
		renderDiscovery(back, &UIState{Screen: ScreenDiscovery, ScanDone: true})
		return back
	}

	version.Version = "v9.9.9"
	a := render()
	version.Version = "v1.1.1"
	b := render()

	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("the discovery screen renders identically for two different builds")
	}
}

// Whatever the version string is, the footer must stay on screen: a dirty
// working tree produces something far longer than a tag.
func TestRenderDiscovery_LongVersionStaysOnScreen(t *testing.T) {
	orig := version.Version
	t.Cleanup(func() { version.Version = orig })
	version.Version = "v0.3.1-14-gdeadbee-dirty"

	for _, w := range []int{640, 800, 1024, 1280, 1920} {
		footer := TextWidth(gitURL+buildSep+version.Version, false)
		if footer > w-2*padding {
			t.Errorf("w=%d: footer line is %dpx and does not fit", w, footer)
		}
	}
}

// A list of bare IPs gets a narrower card: with no reverse DNS the address
// column would repeat the name column, and paying for it leaves the card mostly
// whitespace around fifteen characters of text.
func TestLayoutDiscovery_NarrowsWhenThereAreNoHostnames(t *testing.T) {
	screen := image.Rect(0, 0, 1280, 800)

	named := make([]domain.Host, 3)
	bare := make([]domain.Host, 3)
	for i := range named {
		ip := net.IPv4(10, 0, 0, byte(i+1))
		named[i] = domain.Host{IP: ip, Hostname: "dc0" + string(rune('1'+i))}
		bare[i] = domain.Host{IP: ip}
	}

	wide := layoutDiscoveryFor(screen, named)
	narrow := layoutDiscoveryFor(screen, bare)

	if !wide.Address {
		t.Error("a list with hostnames should carry the address column")
	}
	if narrow.Address {
		t.Error("a list of bare IPs should not carry the address column")
	}
	if narrow.Card.Dx() >= wide.Card.Dx() {
		t.Errorf("narrow card is %dpx, wide is %dpx — it should be smaller",
			narrow.Card.Dx(), wide.Card.Dx())
	}
	if left, right := narrow.Card.Min.X, screen.Max.X-narrow.Card.Max.X; left != right {
		t.Errorf("narrow card is not centred (%d left, %d right)", left, right)
	}
}

// Run sizes its scrolling window from layoutDiscovery, but hit-tests clicks
// against the layout for the hosts actually on screen. If the card's width
// could change how many rows fit, those two would disagree and a click would
// select a different host from the one under the pointer.
func TestLayoutDiscovery_RowCountIsIndependentOfWidth(t *testing.T) {
	for _, s := range [][2]int{{640, 480}, {1024, 768}, {1280, 800}, {1920, 1080}} {
		screen := image.Rect(0, 0, s[0], s[1])
		wide := layoutDiscoveryRows(screen, 1<<30, true)
		narrow := layoutDiscoveryRows(screen, 1<<30, false)

		if wide.Rows != narrow.Rows {
			t.Errorf("%dx%d: %d rows wide, %d narrow", s[0], s[1], wide.Rows, narrow.Rows)
		}
		if wide.RowH != narrow.RowH || wide.List.Min.Y != narrow.List.Min.Y {
			t.Errorf("%dx%d: the rows do not start at the same place", s[0], s[1])
		}
	}
}
