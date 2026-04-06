package ui

import (
	"fmt"
	"image"
)

// spinnerFrames are the animation frames for the connecting spinner.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// renderConnecting draws a "Connecting…" overlay with a spinner.
// The spinnerTick field cycles the animation.
func renderConnecting(img *image.RGBA, state *UIState) {
	bounds := img.Bounds()
	cx := bounds.Max.X / 2
	cy := bounds.Max.Y / 2

	// Dim overlay.
	FillRectBlend(img, bounds, ColorOverlay)

	// Connecting box.
	boxW := 320
	boxH := 80
	r := image.Rect(cx-boxW/2, cy-boxH/2, cx+boxW/2, cy+boxH/2)
	FillRect(img, r, ColorPanel)
	DrawBorder(img, r, ColorAccent)

	// Spinner + message.
	host := ""
	if h := state.SelectedHost(); h != nil {
		host = h.IP.String()
	}
	msg := fmt.Sprintf("Bağlanıyor... %s", host)
	DrawText(img, cx-TextWidth(msg, false)/2, cy-8, msg, ColorText)

	spinner := spinnerFrames[state.SpinnerTick%len(spinnerFrames)]
	DrawText(img, cx-4, cy+10, spinner, ColorAccent)
}
