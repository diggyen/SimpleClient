package ui

import (
	"fmt"
	"image"
)

const (
	barH    = 28 // top/bottom bar height in pixels
	rowH    = 22 // height per host row
	padding = 8  // general padding
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
	DrawTextLarge(img, padding, 4, "rdpboot", ColorAccent)

	// Host count.
	hostCount := fmt.Sprintf("%d sunucu bulundu", len(state.Hosts))
	DrawText(img, w/2-TextWidth(hostCount, false)/2, 8, hostCount, ColorText)

	// Scan status (right side).
	scanStatus := "Taranıyor..."
	if state.ScanDone {
		scanStatus = "Tarama tamamlandı"
	}
	DrawText(img, w-TextWidth(scanStatus, false)-padding, 8, scanStatus, ColorMuted)

	// ── Host list area ───────────────────────────────────────────────────────
	listTop := barH + 2
	listBottom := h - barH - 2
	listH := listBottom - listTop
	maxRows := listH / rowH

	visible := state.VisibleHosts(maxRows)

	if len(visible) == 0 {
		msg := "Sunucu bulunamadı"
		if !state.ScanDone {
			msg = "Taranıyor..."
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
	hints := "↑↓ Seç   Enter Bağlan   F5 Yenile   Q Çıkış"
	DrawText(img, padding, botTop+8, hints, ColorMuted)

	// GitHub URL (right, above progress bar).
	gitURL := "github.com/diggyen/SimpleClient"
	DrawText(img, w-TextWidth(gitURL, false)-padding, botTop+8, gitURL, ColorAccent)

	// Progress bar (right side of bottom bar).
	pbRect := image.Rect(w-202, botTop+8, w-padding, botTop+barH-8)
	DrawProgressBar(img, pbRect, state.ScanProgress, ColorAccent, ColorBorder)

	// Error message banner above bottom bar.
	if state.ErrorMsg != "" {
		FillRect(img, image.Rect(0, botTop-20, w, botTop), ColorError)
		DrawText(img, padding, botTop-16, state.ErrorMsg, ColorText)
	}
}
