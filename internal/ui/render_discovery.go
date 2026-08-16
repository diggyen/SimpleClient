package ui

import (
	"fmt"
	"image"
	"strings"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	padding = 8 // general padding

	gitURL = "github.com/diggyen/SimpleClient"
)

// Discovery card metrics. The card is centred rather than stretched edge to
// edge: a full-width list on a 1280px screen puts the hostname and its latency
// so far apart that they stop reading as one row.
const (
	cardMaxW    = 860
	cardMinW    = 440
	cardMaxH    = 620
	cardMargin  = 48 // minimum gap between the card and the screen edge
	cardHeader  = 44
	cardColumns = 22 // the column-heading strip between the header and the rows
	cardFooter  = 56
	cardRowH    = 28
	cardPad     = 18
	cardMinRows = 6 // keeps the card from collapsing around one or two hosts
	scrollBarW  = 4
	logoTop     = 44 // top of the header block
	logoGap     = 40 // between the header block and the card
	hintsBottom = 52 // distance from the bottom of the screen to the key hints
)

// Column geometry, in character cells from the left edge of the list.
const (
	colNameChars    = 24 // hostname column, wide enough for a FQDN-ish label
	latencyNumChars = 7  // "9999 ms" — a fixed column, so the bars stay aligned
	meterGap        = 9  // between the signal bars and the number
)

// discoveryLayout is the single source of truth for where the discovery screen
// puts things. Rendering and mouse hit-testing both derive from it, so a click
// cannot land on a row the renderer drew somewhere else.
type discoveryLayout struct {
	Screen  image.Rectangle
	Logo    logoLayout
	Card    image.Rectangle
	Header  image.Rectangle
	Columns image.Rectangle
	List    image.Rectangle
	Footer  image.Rectangle
	RowH    int
	Rows    int // how many rows fit in List
}

// layoutDiscovery returns the card at its full height. Run uses it to size the
// scrolling window, which must stay fixed even as the card grows during a scan.
func layoutDiscovery(screen image.Rectangle) discoveryLayout {
	return layoutDiscoveryRows(screen, 1<<30)
}

// layoutDiscoveryFor sizes the card to the number of hosts on offer, so a short
// list does not sit in a mostly empty panel. Once the list is longer than the
// screen allows this is identical to layoutDiscovery, which is why the scroll
// arithmetic stays consistent.
func layoutDiscoveryFor(screen image.Rectangle, hostCount int) discoveryLayout {
	return layoutDiscoveryRows(screen, hostCount)
}

func layoutDiscoveryRows(screen image.Rectangle, rows int) discoveryLayout {
	w, h := screen.Dx(), screen.Dy()

	cardW := clampInt(w-2*cardMargin, cardMinW, cardMaxW)

	// The band the wordmark and the card share, above the pinned key hints.
	regionTop := logoTop
	regionBottom := h - hintsBottom - CharH - 20
	logoH := logoHeight(w)

	avail := regionBottom - regionTop - logoH - logoGap
	if avail > cardMaxH {
		avail = cardMaxH
	}

	fixed := cardHeader + cardColumns + cardFooter + cardPad
	maxRows := (avail - fixed) / cardRowH
	if maxRows < 1 {
		maxRows = 1
	}
	rows = clampInt(rows, cardMinRows, maxRows)

	cardH := fixed + rows*cardRowH

	// The wordmark and the card are centred as one stack, not placed
	// independently. Pinning the wordmark to the top and centring the card in
	// what was left over opened a gap between them that grew with the screen,
	// until the two read as unrelated objects rather than as one page.
	stackH := logoH + logoGap + cardH
	stackTop := regionTop + (regionBottom-regionTop-stackH)/2
	if stackTop < regionTop {
		stackTop = regionTop
	}

	logo := layoutLogo(w, stackTop)
	card := image.Rect(0, 0, cardW, cardH).
		Add(image.Pt((w-cardW)/2, logo.Bounds.Max.Y+logoGap))

	l := discoveryLayout{
		Screen: screen,
		Logo:   logo,
		Card:   card,
		Header: image.Rect(card.Min.X, card.Min.Y, card.Max.X, card.Min.Y+cardHeader),
		Footer: image.Rect(card.Min.X, card.Max.Y-cardFooter, card.Max.X, card.Max.Y),
		RowH:   cardRowH,
		Rows:   rows,
	}
	l.Columns = image.Rect(card.Min.X+1, l.Header.Max.Y, card.Max.X-1, l.Header.Max.Y+cardColumns)
	l.List = image.Rect(
		card.Min.X+1, l.Columns.Max.Y+cardPad/2,
		card.Max.X-1, l.Columns.Max.Y+cardPad/2+rows*cardRowH,
	)
	return l
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}

// rowRect returns the screen rectangle of the i'th visible row.
func (l discoveryLayout) rowRect(i int) image.Rectangle {
	y := l.List.Min.Y + i*l.RowH
	return image.Rect(l.List.Min.X, y, l.List.Max.X, y+l.RowH)
}

// rowAt maps a screen coordinate to a visible row index, or -1.
func (l discoveryLayout) rowAt(x, y int) int {
	if !image.Pt(x, y).In(l.List) {
		return -1
	}
	i := (y - l.List.Min.Y) / l.RowH
	if i < 0 || i >= l.Rows {
		return -1
	}
	return i
}

// renderDiscovery draws the main host-discovery screen.
func renderDiscovery(img *image.RGBA, state *UIState) {
	l := layoutDiscoveryFor(img.Bounds(), len(state.Hosts))

	FillRect(img, l.Screen, ColorBG)

	drawLogo(img, l.Logo)
	drawLanguageBadge(img, l.Screen)

	DrawPanel(img, l.Card, ColorPanel, ColorBorder)

	renderCardHeader(img, l, state)
	renderColumnHeadings(img, l, state)

	visible := state.VisibleHosts(l.Rows)
	if len(visible) == 0 {
		renderEmptyList(img, l, state)
	} else {
		renderHostRows(img, l, state, visible)
	}

	renderCardFooter(img, l, state)
	renderErrorBanner(img, l, state)
	renderKeyHints(img, l)
}

// drawLanguageBadge puts the active language in the top-right corner as a cap,
// so it looks like the F2-toggled control it is rather than stray text.
func drawLanguageBadge(img *image.RGBA, screen image.Rectangle) {
	code := strings.ToUpper(string(i18n.Current()))
	w := TextWidth(code, false) + 10
	DrawKeycap(img, screen.Max.X-padding*3-w, padding*2, code)
}

// renderCardHeader draws the card's title strip.
func renderCardHeader(img *image.RGBA, l discoveryLayout, state *UIState) {
	strip := image.Rect(l.Header.Min.X+1, l.Header.Min.Y+1, l.Header.Max.X-1, l.Header.Max.Y)
	FillNotchedTop(img, strip, ColorCardHeader)
	// Restore the panel's lit top edge, which the strip has just painted over.
	DrawHLine(img, strip.Min.X+2, strip.Max.X-2, strip.Min.Y, ColorPanelEdge)
	DrawHLine(img, strip.Min.X, strip.Max.X, strip.Max.Y-1, ColorBorder)

	textY := l.Header.Min.Y + (cardHeader-CharH)/2

	// A short accent tick ahead of the title. It ties the card to the mark and
	// gives the title a left edge to sit against.
	FillRect(img, image.Rect(
		l.Header.Min.X+cardPad, textY+1,
		l.Header.Min.X+cardPad+3, textY+CharH-1,
	), ColorAccent)

	DrawText(img, l.Header.Min.X+cardPad+11, textY, i18n.T(i18n.SelectServer), ColorText)

	count := i18n.HostCount(len(state.Hosts))
	DrawText(img, l.Header.Max.X-cardPad-TextWidth(count, false), textY, count, ColorDim)
}

// renderColumnHeadings labels the three columns the rows are set in.
func renderColumnHeadings(img *image.RGBA, l discoveryLayout, state *UIState) {
	y := l.Columns.Min.Y + (cardColumns-CharH)/2 + 1
	const tracking = 1

	DrawTextTracked(img, l.List.Min.X+cardPad+11, y, i18n.T(i18n.ColumnServer), tracking, ColorDim)
	DrawTextTracked(img, l.List.Min.X+cardPad+11+colNameChars*CharW, y,
		i18n.T(i18n.ColumnAddress), tracking, ColorDim)

	latency := i18n.T(i18n.ColumnLatency)
	DrawTextTracked(img, l.rightEdge(state)-TrackedWidth(latency, tracking), y,
		latency, tracking, ColorDim)

	DrawHLine(img, l.Columns.Min.X+cardPad, l.Columns.Max.X-cardPad, l.Columns.Max.Y-1, ColorBorder)
}

// rightEdge is where the latency column ends, kept clear of the scrollbar when
// the list has one.
func (l discoveryLayout) rightEdge(state *UIState) int {
	edge := l.List.Max.X - cardPad
	if len(state.Hosts) > l.Rows {
		edge -= scrollBarW + 6
	}
	return edge
}

// renderHostRows draws the visible slice of the host list.
func renderHostRows(img *image.RGBA, l discoveryLayout, state *UIState, visible []domain.Host) {
	markerX := l.List.Min.X + cardPad
	nameX := markerX + 11
	ipX := nameX + colNameChars*CharW
	rightEdge := l.rightEdge(state)

	for i, host := range visible {
		absIdx := state.ScrollOffset + i
		row := l.rowRect(i)
		selected := absIdx == state.SelectedIdx

		switch {
		case selected:
			FillRect(img, row, ColorSelected)
			// An accent bar on the leading edge marks the selection more
			// clearly than a border on a low-contrast display.
			FillRect(img, image.Rect(row.Min.X, row.Min.Y, row.Min.X+3, row.Max.Y), ColorAccent)
		case absIdx%2 == 1:
			// Band by absolute index so the stripes do not flip as the list
			// scrolls under the selection.
			FillRect(img, row, ColorRowHover)
		}

		textY := row.Min.Y + (l.RowH-CharH)/2

		nameColor, ipColor := ColorMuted, ColorDim
		if selected {
			nameColor, ipColor = ColorText, ColorMuted
			DrawChevron(img, markerX+1, textY+3, ColorAccent)
		}

		DrawText(img, nameX, textY, host.DisplayName(), nameColor)

		// The IP is redundant when it is already the display name.
		if host.Hostname != "" && ipX+TextWidth(host.IP.String(), false) < rightEdge-90 {
			DrawText(img, ipX, textY, host.IP.String(), ipColor)
		}

		if host.LatencyMs > 0 {
			drawLatency(img, rightEdge, textY, host.LatencyMs)
		}
	}

	drawScrollBar(img, l, len(state.Hosts), state.ScrollOffset)
}

// drawLatency renders one host's round-trip time as a number with a signal
// staircase beside it, right-aligned to edge. The bars make the column
// comparable at a glance; the number is for when the difference matters.
func drawLatency(img *image.RGBA, edge, textY int, ms int64) {
	c := LatencyColor(ms)

	// The number is right-aligned inside a fixed column instead of against the
	// card edge, so the staircases line up down the list rather than stepping
	// left and right with the width of each number.
	label := fmt.Sprintf("%d ms", ms)
	DrawText(img, edge-TextWidth(label, false), textY, label, c)

	sigX := edge - latencyNumChars*CharW - meterGap - SignalWidth
	DrawSignal(img, image.Rect(sigX, textY+1, sigX+SignalWidth, textY+CharH-1),
		latencyStrength(ms), c)
}

// latencyStrength grades a round-trip time onto the signal staircase, on the
// same thresholds LatencyColor uses so the bars and the colour never disagree.
func latencyStrength(ms int64) int {
	switch {
	case ms <= 0:
		return 0
	case ms < 20:
		return 4
	case ms < 50:
		return 3
	case ms < 150:
		return 2
	default:
		return 1
	}
}

// renderEmptyList draws the placeholder shown before anything is found.
func renderEmptyList(img *image.RGBA, l discoveryLayout, state *UIState) {
	cx := l.List.Min.X + l.List.Dx()/2
	cy := l.List.Min.Y + l.List.Dy()/2

	if !state.ScanDone {
		const cell = 9
		DrawSpinner(img, cx-SpinnerSize(cell)/2, cy-SpinnerSize(cell)-4,
			state.SpinnerTick, cell, ColorAccent)

		msg := i18n.T(i18n.Scanning)
		DrawText(img, cx-TextWidth(msg, false)/2, cy+8, msg, ColorMuted)
		return
	}

	if msg := strings.ToUpper(i18n.T(i18n.NoServersFound)); PixelTextCovers(msg) {
		DrawPixelText(img, cx-PixelTextWidth(msg, 2)/2, cy-2*CharH, msg, 2, ColorMuted)
	} else {
		msg := i18n.T(i18n.NoServersFound)
		DrawTextLarge(img, cx-TextWidth(msg, true)/2, cy-2*CharH, msg, ColorMuted)
	}

	// Wrap the explanation so it never runs past the card.
	hint := i18n.T(i18n.NoServersHint)
	maxChars := (l.List.Dx() - 2*cardPad) / CharW
	for n, line := range wrapText(hint, maxChars) {
		DrawText(img, cx-TextWidth(line, false)/2, cy+12+n*(CharH+4), line, ColorDim)
	}
}

// renderCardFooter draws the scan status and its progress meter.
func renderCardFooter(img *image.RGBA, l discoveryLayout, state *UIState) {
	DrawHLine(img, l.Footer.Min.X+1, l.Footer.Max.X-1, l.Footer.Min.Y, ColorBorder)

	status := i18n.T(i18n.Scanning)
	statusColor := ColorMuted
	if state.ScanDone {
		status = i18n.T(i18n.ScanComplete)
		statusColor = ColorSuccess
	}

	// A state dot ahead of the label: on a card this dense the colour of a word
	// is easy to miss, a filled square next to it is not.
	dotY := l.Footer.Min.Y + 15
	FillRect(img, image.Rect(l.Footer.Min.X+cardPad, dotY, l.Footer.Min.X+cardPad+6, dotY+6), statusColor)
	DrawText(img, l.Footer.Min.X+cardPad+14, l.Footer.Min.Y+12, status, statusColor)

	pct := fmt.Sprintf("%d%%", int(state.ScanProgress*100))
	DrawText(img, l.Footer.Max.X-cardPad-TextWidth(pct, false), l.Footer.Min.Y+12, pct, ColorDim)

	bar := image.Rect(
		l.Footer.Min.X+cardPad, l.Footer.Min.Y+34,
		l.Footer.Max.X-cardPad, l.Footer.Min.Y+40,
	)
	DrawProgressBar(img, bar, state.ScanProgress, ColorAccent, ColorTrack)
}

// renderErrorBanner draws the transient message strip under the card.
func renderErrorBanner(img *image.RGBA, l discoveryLayout, state *UIState) {
	if state.ErrorMsg == "" {
		return
	}
	banner := image.Rect(l.Card.Min.X, l.Card.Max.Y+14, l.Card.Max.X, l.Card.Max.Y+46)
	FillNotched(img, banner, ColorErrorBorder)
	FillNotched(img, banner.Inset(1), ColorErrorBG)

	textY := banner.Min.Y + (banner.Dy()-CharH)/2

	// The leading bar gives the banner the same anatomy as a selected row.
	FillRect(img, image.Rect(banner.Min.X+2, banner.Min.Y+2, banner.Min.X+5, banner.Max.Y-2), ColorError)
	FillRect(img, image.Rect(banner.Min.X+cardPad, textY+1, banner.Min.X+cardPad+5, textY+6), ColorError)
	DrawText(img, banner.Min.X+cardPad+13, textY, state.ErrorMsg, ColorText)
}

// keyHints pairs each key with the action it performs, so the footer can set
// the key as a cap and the action as plain text.
func keyHints() [][2]string {
	return [][2]string{
		{"↑↓", i18n.T(i18n.HintSelect)},
		{"Enter", i18n.T(i18n.HintConnect)},
		{"F5", i18n.T(i18n.HintRefresh)},
		{"F2", i18n.T(i18n.HintLanguage)},
	}
}

// keyHintsWidth measures the hint row so it can be centred.
func keyHintsWidth() int {
	const capGap, pairGap = 7, 22
	w := 0
	for i, h := range keyHints() {
		if i > 0 {
			w += pairGap
		}
		w += TextWidth(h[0], false) + 10 + capGap + TextWidth(h[1], false)
	}
	return w
}

// renderKeyHints draws the bottom key legend and the project URL.
func renderKeyHints(img *image.RGBA, l discoveryLayout) {
	const capGap, pairGap = 7, 22

	y := l.Screen.Max.Y - hintsBottom
	x := l.Screen.Min.X + (l.Screen.Dx()-keyHintsWidth())/2

	for _, h := range keyHints() {
		x += DrawKeycap(img, x, y, h[0]) + capGap
		DrawText(img, x, y+4, h[1], ColorDim)
		x += TextWidth(h[1], false) + pairGap
	}

	urlY := y + CharH + 18
	DrawText(img, l.Screen.Min.X+(l.Screen.Dx()-TextWidth(gitURL, false))/2, urlY, gitURL, ColorFaint)
}

// drawScrollBar shows the visible portion of a list too long for the card.
func drawScrollBar(img *image.RGBA, l discoveryLayout, total, offset int) {
	if total <= l.Rows || l.Rows == 0 {
		return
	}

	track := image.Rect(l.List.Max.X-scrollBarW-2, l.List.Min.Y, l.List.Max.X-2, l.List.Max.Y)
	FillRect(img, track, ColorTrack)

	thumbH := track.Dy() * l.Rows / total
	if thumbH < 16 {
		thumbH = 16
	}
	maxOffset := total - l.Rows
	thumbY := track.Min.Y
	if maxOffset > 0 {
		thumbY += (track.Dy() - thumbH) * offset / maxOffset
	}
	FillRect(img, image.Rect(track.Min.X, thumbY, track.Max.X, thumbY+thumbH), ColorScrollBar)
}

// wrapText breaks s into lines of at most width characters, on word boundaries.
func wrapText(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var (
		lines []string
		line  string
	)
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
