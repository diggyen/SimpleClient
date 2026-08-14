package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	modalW = 400
	modalH = 280
)

// renderModal draws the credential entry modal over the current screen.
func renderModal(img *image.RGBA, state *UIState) {
	bounds := img.Bounds()
	cx := bounds.Max.X / 2
	cy := bounds.Max.Y / 2

	// Semi-transparent dark overlay.
	FillRectBlend(img, bounds, ColorOverlay)

	// Modal box.
	r := image.Rect(cx-modalW/2, cy-modalH/2, cx+modalW/2, cy+modalH/2)
	FillRect(img, r, ColorPanel)
	DrawBorder(img, r, ColorAccent)

	// Title bar.
	titleBar := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+barH)
	FillRect(img, titleBar, ColorBar)
	DrawText(img, r.Min.X+padding, r.Min.Y+8, i18n.T(i18n.ModalTitle), ColorText)
	DrawHLine(img, r.Min.X, r.Max.X, r.Min.Y+barH, ColorBorder)

	// Field labels and inputs. Labels are localised and therefore vary in width;
	// the input column starts at a fixed offset so they stay aligned.
	labels := [3]string{
		i18n.T(i18n.LabelUsername),
		i18n.T(i18n.LabelPassword),
		i18n.T(i18n.LabelDomain),
	}
	fieldH := 22
	fieldXLabel := r.Min.X + padding
	fieldXInput := r.Min.X + 90
	fieldW := r.Max.X - fieldXInput - padding

	for i, label := range labels {
		y := r.Min.Y + barH + 20 + i*(fieldH+14)
		DrawText(img, fieldXLabel, y+4, label, ColorMuted)
		fRect := image.Rect(fieldXInput, y, fieldXInput+fieldW, y+fieldH)
		text := state.Modal.Fields[i]
		// Mask password field.
		if i == 1 {
			text = maskStr(state.Modal.Fields[i])
		}
		DrawInputField(img, fRect, text, state.Modal.FocusIdx == i)
	}

	// Error message.
	if state.Modal.Error != "" {
		errY := r.Min.Y + barH + 20 + 3*(fieldH+14) + 4
		DrawText(img, fieldXLabel, errY, state.Modal.Error, ColorError)
	}

	// Buttons.
	btnY := r.Max.Y - 44
	btnW := 120
	btnH := 30
	gap := 16

	connectRect := image.Rect(cx-btnW-gap/2, btnY, cx-gap/2, btnY+btnH)
	cancelRect := image.Rect(cx+gap/2, btnY, cx+btnW+gap/2, btnY+btnH)

	connectColor := ColorAccent
	if state.Modal.FocusIdx == 3 {
		connectColor = ColorFocus
	}
	cancelColor := ColorBorder
	if state.Modal.FocusIdx == 4 {
		cancelColor = ColorFocus
	}

	connectLabel := i18n.T(i18n.ButtonConnect)
	cancelLabel := i18n.T(i18n.ButtonCancel)

	FillRect(img, connectRect, connectColor)
	DrawText(img, connectRect.Min.X+(btnW-TextWidth(connectLabel, false))/2,
		connectRect.Min.Y+8, connectLabel, ColorText)

	FillRect(img, cancelRect, ColorPanel)
	DrawBorder(img, cancelRect, cancelColor)
	DrawText(img, cancelRect.Min.X+(btnW-TextWidth(cancelLabel, false))/2,
		cancelRect.Min.Y+8, cancelLabel, ColorMuted)
}

func maskStr(s string) string {
	out := make([]rune, len([]rune(s)))
	for i := range out {
		out[i] = '•'
	}
	return string(out)
}
