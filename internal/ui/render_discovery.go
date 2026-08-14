package ui

import (
	"fmt"
	"image"
	"strings"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	barH    = 28 // top/bottom bar height in pixels
	rowH    = 22 // height per host row
	padding = 8  // general padding

	progressBarW = 194 // width of the scan progress bar in the bottom bar
	gitURL       = "github.com/diggyen/SimpleClient"
)

// renderDiscovery draws the main host-discovery screen.
func renderDiscovery(img *image.RGBA, state *UIState) {
	bounds := img.Bounds()
	w := bounds.Max.X
	h := bounds.Max.Y

	// Clear background.
	FillRect(img, bounds, ColorBG)

	// ── Top bar ──────────────────────────────────────────────────────────────
	topBar := image.Rect(0, 0, w, barH)
	FillRect(img, topBar, ColorBar)
	DrawHLine(img, 0, w, barH-1, ColorBorder)

	// Logo / title.
	DrawTextLarge(img, padding, 4, "SimpleClient", ColorAccent)

	// Host count.
	hostCount := i18n.Tf(i18n.HostsFound, len(state.Hosts))
	DrawText(img, w/2-TextWidth(hostCount, false)/2, 8, hostCount, ColorText)

	// Active language badge, far right.
	langBadge := "[" + strings.ToUpper(string(i18n.Current())) + "]"
	langX := w - TextWidth(langBadge, false) - padding
	DrawText(img, langX, 8, langBadge, ColorAccent)

	// Scan status, left of the language badge.
	scanStatus := i18n.T(i18n.Scanning)
	if state.ScanDone {
		scanStatus = i18n.T(i18n.ScanComplete)
	}
	DrawText(img, langX-TextWidth(scanStatus, false)-padding, 8, scanStatus, ColorMuted)

	// ── Host list area ───────────────────────────────────────────────────────
	listTop := barH + 2
	listBottom := h - barH - 2
	listH := listBottom - listTop
	maxRows := listH / rowH

	visible := state.VisibleHosts(maxRows)

	if len(visible) == 0 {
		msg := i18n.T(i18n.NoServersFound)
		if !state.ScanDone {
			msg = i18n.T(i18n.Scanning)
		}
		DrawTextLarge(img, w/2-TextWidth(msg, true)/2, h/2-13, msg, ColorMuted)
	} else {
		for i, host := range visible {
			absIdx := state.ScrollOffset + i
			y := listTop + i*rowH

			rowRect := image.Rect(padding, y, w-padding, y+rowH-1)

			// Highlight selected row.
			if absIdx == state.SelectedIdx {
				FillRect(img, rowRect, ColorSelected)
				DrawBorder(img, rowRect, ColorAccent)
			}

			// IP / hostname.
			DrawText(img, padding+4, y+4, host.DisplayName(), ColorText)

			// IP (if hostname shown).
			if host.Hostname != "" {
				DrawText(img, padding+200, y+4, host.IP.String(), ColorMuted)
			}

			// Latency.
			if host.LatencyMs > 0 {
				latStr := fmt.Sprintf("%dms", host.LatencyMs)
				DrawText(img, w-padding-TextWidth(latStr, false)-4, y+4, latStr, ColorLatency)
			}
		}
	}

	// ── Scroll indicators ────────────────────────────────────────────────────
	if state.ScrollOffset > 0 {
		DrawText(img, w/2-10, listTop, "▲", ColorMuted)
	}
	if len(state.Hosts) > state.ScrollOffset+maxRows {
		DrawText(img, w/2-10, listBottom-CharH, "▼", ColorMuted)
	}

	// ── Bottom bar ───────────────────────────────────────────────────────────
	botTop := h - barH
	FillRect(img, image.Rect(0, botTop, w, h), ColorBar)
	DrawHLine(img, 0, w, botTop, ColorBorder)

	// Key hints (left).
	hints := i18n.T(i18n.KeyHints)
	hintsEnd := padding + TextWidth(hints, false)
	DrawText(img, padding, botTop+8, hints, ColorMuted)

	// Progress bar (right).
	pbRect := image.Rect(w-progressBarW-padding, botTop+8, w-padding, botTop+barH-8)
	DrawProgressBar(img, pbRect, state.ScanProgress, ColorAccent, ColorBorder)

	// Project URL, centred in whatever space is left between the two. Dropped
	// entirely when it would collide — the hint text is localised and its width
	// changes with the language.
	urlW := TextWidth(gitURL, false)
	urlX := (hintsEnd + pbRect.Min.X - urlW) / 2
	if urlX > hintsEnd+padding && urlX+urlW < pbRect.Min.X-padding {
		DrawText(img, urlX, botTop+8, gitURL, ColorAccent)
	}

	// Error message banner above bottom bar.
	if state.ErrorMsg != "" {
		FillRect(img, image.Rect(0, botTop-20, w, botTop), ColorError)
		DrawText(img, padding, botTop-16, state.ErrorMsg, ColorText)
	}
}
