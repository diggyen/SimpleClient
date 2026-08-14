package ui

import (
	"fmt"
	"image"
	"strings"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	barH    = 28 // modal title bar height
	rowH    = 22 // legacy row height, still used by the modal's field metrics
	padding = 8  // general padding

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
	cardHeader  = 42
	cardFooter  = 54
	cardRowH    = 28
	cardPad     = 18
	cardMinRows = 8 // keeps the card from collapsing around one or two hosts
	scrollBarW  = 4
	brandTop    = 34 // top of the wordmark above the card
	hintsBottom = 42 // distance from the bottom of the screen to the key hints
)

// discoveryLayout is the single source of truth for where the discovery screen
// puts things. Rendering and mouse hit-testing both derive from it, so a click
// cannot land on a row the renderer drew somewhere else.
type discoveryLayout struct {
	Screen image.Rectangle
	Card   image.Rectangle
	Header image.Rectangle
	List   image.Rectangle
	Footer image.Rectangle
	RowH   int
	Rows   int // how many rows fit in List
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

	// The band left between the wordmark and the key hints.
	top := brandTop + 2*CharH + 24 // the wordmark is drawn at 2x scale
	bottom := h - hintsBottom - CharH - 10

	avail := bottom - top
	if avail > cardMaxH {
		avail = cardMaxH
	}

	fixed := cardHeader + cardFooter + cardPad
	maxRows := (avail - fixed) / cardRowH
	if maxRows < 1 {
		maxRows = 1
	}
	rows = clampInt(rows, cardMinRows, maxRows)

	cardH := fixed + rows*cardRowH
	card := image.Rect(0, 0, cardW, cardH).
		Add(image.Pt((w-cardW)/2, top+(avail-cardH)/2))

	l := discoveryLayout{
		Screen: screen,
		Card:   card,
		Header: image.Rect(card.Min.X, card.Min.Y, card.Max.X, card.Min.Y+cardHeader),
		Footer: image.Rect(card.Min.X, card.Max.Y-cardFooter, card.Max.X, card.Max.Y),
		RowH:   cardRowH,
		Rows:   rows,
	}
	l.List = image.Rect(
		card.Min.X+1, l.Header.Max.Y+cardPad/2,
		card.Max.X-1, l.Header.Max.Y+cardPad/2+rows*cardRowH,
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
	w := l.Screen.Dx()

	FillRect(img, l.Screen, ColorBG)

	// ── Wordmark and language badge ──────────────────────────────────────────
	brand := "SimpleClient"
	DrawTextLarge(img, w/2-TextWidth(brand, true)/2, brandTop, brand, ColorAccent)

	badge := "[" + strings.ToUpper(string(i18n.Current())) + "]"
	DrawText(img, w-TextWidth(badge, false)-padding*2, padding*2, badge, ColorAccent)

	// ── Card ─────────────────────────────────────────────────────────────────
	// A translucent offset rectangle reads as a shadow and lifts the card off
	// the background without needing real blur.
	FillRectBlend(img, l.Card.Add(image.Pt(4, 5)), ColorCardShadow)
	FillRect(img, l.Card, ColorPanel)
	DrawBorder(img, l.Card, ColorBorder)

	FillRect(img, l.Header, ColorCardHeader)
	DrawHLine(img, l.Header.Min.X, l.Header.Max.X, l.Header.Max.Y-1, ColorBorder)

	title := i18n.T(i18n.SelectServer)
	DrawText(img, l.Header.Min.X+cardPad, l.Header.Min.Y+(cardHeader-CharH)/2-3, title, ColorText)

	count := i18n.HostCount(len(state.Hosts))
	DrawText(img, l.Header.Max.X-cardPad-TextWidth(count, false),
		l.Header.Min.Y+(cardHeader-CharH)/2-3, count, ColorDim)

	// ── Rows ─────────────────────────────────────────────────────────────────
	visible := state.VisibleHosts(l.Rows)
	if len(visible) == 0 {
		renderEmptyList(img, l, state)
	} else {
		renderHostRows(img, l, state, visible)
	}

	// ── Footer: scan status and progress ─────────────────────────────────────
	DrawHLine(img, l.Footer.Min.X, l.Footer.Max.X, l.Footer.Min.Y, ColorBorder)

	status := i18n.T(i18n.Scanning)
	statusColor := ColorDim
	if state.ScanDone {
		status = i18n.T(i18n.ScanComplete)
		statusColor = ColorSuccess
	}
	DrawText(img, l.Footer.Min.X+cardPad, l.Footer.Min.Y+14, status, statusColor)

	pct := fmt.Sprintf("%d%%", int(state.ScanProgress*100))
	DrawText(img, l.Footer.Max.X-cardPad-TextWidth(pct, false), l.Footer.Min.Y+14, pct, ColorDim)

	bar := image.Rect(
		l.Footer.Min.X+cardPad, l.Footer.Min.Y+34,
		l.Footer.Max.X-cardPad, l.Footer.Min.Y+38,
	)
	DrawProgressBar(img, bar, state.ScanProgress, ColorAccent, ColorBorder)

	// ── Error banner ─────────────────────────────────────────────────────────
	if state.ErrorMsg != "" {
		banner := image.Rect(l.Card.Min.X, l.Card.Max.Y+10, l.Card.Max.X, l.Card.Max.Y+34)
		FillRect(img, banner, ColorError)
		DrawText(img, banner.Min.X+cardPad, banner.Min.Y+5, state.ErrorMsg, ColorText)
	}

	// ── Key hints ────────────────────────────────────────────────────────────
	hints := i18n.T(i18n.KeyHints)
	hintY := l.Screen.Max.Y - hintsBottom
	DrawText(img, w/2-TextWidth(hints, false)/2, hintY, hints, ColorMuted)
	DrawText(img, w/2-TextWidth(gitURL, false)/2, hintY+CharH+7, gitURL, ColorDim)
}

// renderHostRows draws the visible slice of the host list.
func renderHostRows(img *image.RGBA, l discoveryLayout, state *UIState, visible []domain.Host) {
	nameX := l.List.Min.X + cardPad
	ipX := nameX + 26*CharW

	// Keep the latency column clear of the scrollbar when there is one.
	rightEdge := l.List.Max.X - cardPad
	scrolling := len(state.Hosts) > l.Rows
	if scrolling {
		rightEdge -= scrollBarW + 6
	}

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

		DrawText(img, nameX, textY, host.DisplayName(), ColorText)

		// The IP is redundant when it is already the display name.
		if host.Hostname != "" && ipX+TextWidth(host.IP.String(), false) < rightEdge-90 {
			DrawText(img, ipX, textY, host.IP.String(), ColorDim)
		}

		if host.LatencyMs > 0 {
			lat := fmt.Sprintf("%d ms", host.LatencyMs)
			DrawText(img, rightEdge-TextWidth(lat, false), textY, lat, LatencyColor(host.LatencyMs))
		}
	}

	drawScrollBar(img, l, len(state.Hosts), state.ScrollOffset)
}

// renderEmptyList draws the placeholder shown before anything is found.
func renderEmptyList(img *image.RGBA, l discoveryLayout, state *UIState) {
	cx := l.List.Min.X + l.List.Dx()/2
	cy := l.List.Min.Y + l.List.Dy()/2

	if !state.ScanDone {
		spin := spinnerFrames[state.SpinnerTick%len(spinnerFrames)]
		DrawTextLarge(img, cx-TextWidth(spin, true)/2, cy-2*CharH, spin, ColorAccent)

		msg := i18n.T(i18n.Scanning)
		DrawText(img, cx-TextWidth(msg, false)/2, cy+8, msg, ColorMuted)
		return
	}

	msg := i18n.T(i18n.NoServersFound)
	DrawTextLarge(img, cx-TextWidth(msg, true)/2, cy-2*CharH, msg, ColorMuted)

	// Wrap the explanation so it never runs past the card.
	hint := i18n.T(i18n.NoServersHint)
	maxChars := (l.List.Dx() - 2*cardPad) / CharW
	for n, line := range wrapText(hint, maxChars) {
		DrawText(img, cx-TextWidth(line, false)/2, cy+10+n*(CharH+4), line, ColorDim)
	}
}

// drawScrollBar shows the visible portion of a list too long for the card.
func drawScrollBar(img *image.RGBA, l discoveryLayout, total, offset int) {
	if total <= l.Rows || l.Rows == 0 {
		return
	}

	track := image.Rect(l.List.Max.X-scrollBarW-2, l.List.Min.Y, l.List.Max.X-2, l.List.Max.Y)
	FillRect(img, track, ColorBorder)

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
