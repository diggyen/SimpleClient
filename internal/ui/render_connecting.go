package ui

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

// renderConnecting draws a "Connecting…" overlay with a spinner.
// The spinnerTick field cycles the animation.
func renderConnecting(img *image.RGBA, state *UIState) {
	bounds := img.Bounds()
	cx := bounds.Max.X / 2
	cy := bounds.Max.Y / 2

	// Dim overlay.
	FillRectBlend(img, bounds, ColorOverlay)

	// Connecting box, styled to match the discovery card and the credential
	// dialog rather than being a plain rectangle.
	const boxW, boxH = 420, 106
	r := image.Rect(cx-boxW/2, cy-boxH/2, cx+boxW/2, cy+boxH/2)
	DrawPanel(img, r, ColorPanel, ColorAccent)
	drawTrack(img, cx, r.Min.Y+30, state.SpinnerTick)

	host := ""
	if h := state.SelectedHost(); h != nil {
		host = h.DisplayName()
	}
	msg := i18n.Tf(i18n.Connecting, host)
	DrawText(img, cx-TextWidth(msg, false)/2, r.Min.Y+62, msg, ColorText)
}

// drawTrack draws a block that runs along a track centred on cx, leaving a
// fading trail. The single spinning character this replaced was 7px of
// movement on a 1280px screen — easy to miss, and easy to mistake for a
// frozen kiosk.
func drawTrack(img *image.RGBA, cx, y, tick int) {
	const (
		cells   = 8
		cellW   = 22
		cellGap = 6
		cellH   = 12
	)
	// Head first, then the two cells it has just left.
	trail := [...]color.RGBA{ColorAccent, ColorAccentDim, ColorAccentFaint}

	trackW := cells*cellW + (cells-1)*cellGap
	trackX := cx - trackW/2
	head := tick % cells

	for i := 0; i < cells; i++ {
		c := ColorTrack
		if dist := (head - i + cells) % cells; dist < len(trail) {
			c = trail[dist]
		}
		x := trackX + i*(cellW+cellGap)
		FillRect(img, image.Rect(x, y, x+cellW, y+cellH), c)
	}
}

// clock is the time the countdown is drawn against; tests fix it.
var clock = time.Now

// renderAuto draws the countdown to logging on to the startup server: the
// connecting box with the server, the seconds left — or what it is waiting
// for, or why the last attempt failed — and the keys that cut it short.
func renderAuto(img *image.RGBA, state *UIState) {
	bounds := img.Bounds()
	cx := bounds.Max.X / 2
	cy := bounds.Max.Y / 2

	FillRectBlend(img, bounds, ColorOverlay)
	const boxW, boxH = 480, 152
	r := image.Rect(cx-boxW/2, cy-boxH/2, cx+boxW/2, cy+boxH/2)
	DrawPanel(img, r, ColorPanel, ColorAccent)
	drawTrack(img, cx, r.Min.Y+30, state.SpinnerTick)

	centred := func(y int, s string, c color.RGBA) {
		DrawText(img, cx-TextWidth(s, false)/2, y, s, c)
	}
	server := state.Modal.Fields[fieldServer]
	if h := state.SelectedHost(); h != nil && h.Address() == server {
		server = h.DisplayName()
	}
	centred(r.Min.Y+58, server, ColorText)

	left := int(math.Ceil(state.AutoAt.Sub(clock()).Seconds()))
	if left < 0 {
		left = 0
	}
	switch {
	case state.AutoErr != "":
		centred(r.Min.Y+80, state.AutoErr, ColorError)
		centred(r.Min.Y+100, i18n.Tf(i18n.AutoRetry, left), ColorDim)
	case !state.AutoNet:
		centred(r.Min.Y+80, i18n.T(i18n.AutoWaitNet), ColorDim)
	default:
		centred(r.Min.Y+80, i18n.Tf(i18n.AutoCountdown, left), ColorDim)
	}
	centred(r.Min.Y+126, i18n.T(i18n.AutoKeys), ColorMuted)
}
