package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

// spinnerFrames are the animation frames for the connecting spinner.
// Kept to ASCII on purpose: the braille frames this used to hold are outside
// Go Mono's glyph coverage and rendered as nothing at all.
var spinnerFrames = []string{"|", "/", "-", "\\"}

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
	const boxW, boxH = 380, 104
	r := image.Rect(cx-boxW/2, cy-boxH/2, cx+boxW/2, cy+boxH/2)
	FillRectBlend(img, r.Add(image.Pt(5, 6)), ColorCardShadow)
	FillRect(img, r, ColorPanel)
	DrawBorder(img, r, ColorAccent)

	spinner := spinnerFrames[state.SpinnerTick%len(spinnerFrames)]
	DrawTextLarge(img, cx-TextWidth(spinner, true)/2, r.Min.Y+18, spinner, ColorAccent)

	host := ""
	if h := state.SelectedHost(); h != nil {
		host = h.DisplayName()
	}
	msg := i18n.Tf(i18n.Connecting, host)
	DrawText(img, cx-TextWidth(msg, false)/2, r.Max.Y-30, msg, ColorText)
}
