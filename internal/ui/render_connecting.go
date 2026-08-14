package ui

import (
	"image"
	"image/color"

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

	// A block that runs along a track, leaving a fading trail. The single
	// spinning character this replaced was 7px of movement on a 1280px screen —
	// easy to miss, and easy to mistake for a frozen kiosk.
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
	trackY := r.Min.Y + 30
	head := state.SpinnerTick % cells

	for i := 0; i < cells; i++ {
		c := ColorTrack
		if dist := (head - i + cells) % cells; dist < len(trail) {
			c = trail[dist]
		}
		x := trackX + i*(cellW+cellGap)
		FillRect(img, image.Rect(x, trackY, x+cellW, trackY+cellH), c)
	}

	host := ""
	if h := state.SelectedHost(); h != nil {
		host = h.DisplayName()
	}
	msg := i18n.Tf(i18n.Connecting, host)
	DrawText(img, cx-TextWidth(msg, false)/2, r.Min.Y+62, msg, ColorText)
}
