package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	modalW      = 480
	modalPad    = 18
	modalFieldH = 26
	modalFieldG = 12 // gap between fields
	modalLabelW = 88 // label column; the widest label is Turkish "Etki alanı:"
	modalBtnH   = 32
	modalBtnW   = 132
	modalBtnGap = 14
	modalErrH   = 20
)

// modalLayout places the credential dialog. Rendering and mouse hit-testing
// share it so a click on a button or field always lands where it looks.
type modalLayout struct {
	Box     image.Rectangle
	Header  image.Rectangle
	Fields  [3]image.Rectangle
	LabelX  int
	ErrorY  int
	Connect image.Rectangle
	Cancel  image.Rectangle
}

func layoutModal(screen image.Rectangle) modalLayout {
	h := cardHeader + modalPad +
		3*modalFieldH + 2*modalFieldG +
		modalErrH + modalPad + modalBtnH + modalPad

	box := image.Rect(0, 0, modalW, h).
		Add(image.Pt((screen.Dx()-modalW)/2, (screen.Dy()-h)/2))

	l := modalLayout{
		Box:    box,
		Header: image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader),
		LabelX: box.Min.X + modalPad,
	}

	inputX := box.Min.X + modalPad + modalLabelW
	inputW := box.Max.X - modalPad - inputX
	y := l.Header.Max.Y + modalPad
	for i := range l.Fields {
		l.Fields[i] = image.Rect(inputX, y, inputX+inputW, y+modalFieldH)
		y += modalFieldH + modalFieldG
	}

	l.ErrorY = y - modalFieldG + 3

	btnY := box.Max.Y - modalPad - modalBtnH
	cx := box.Min.X + box.Dx()/2
	l.Connect = image.Rect(cx-modalBtnW-modalBtnGap/2, btnY, cx-modalBtnGap/2, btnY+modalBtnH)
	l.Cancel = image.Rect(cx+modalBtnGap/2, btnY, cx+modalBtnW+modalBtnGap/2, btnY+modalBtnH)
	return l
}

// renderModal draws the credential entry modal over the current screen.
func renderModal(img *image.RGBA, state *UIState) {
	l := layoutModal(img.Bounds())

	// Dim everything behind it so the dialog is unmistakably the focus.
	FillRectBlend(img, img.Bounds(), ColorOverlay)

	FillRectBlend(img, l.Box.Add(image.Pt(5, 6)), ColorCardShadow)
	FillRect(img, l.Box, ColorPanel)
	DrawBorder(img, l.Box, ColorAccent)

	// ── Header: title, and which host is about to be connected to ────────────
	FillRect(img, l.Header, ColorCardHeader)
	DrawHLine(img, l.Header.Min.X, l.Header.Max.X, l.Header.Max.Y-1, ColorBorder)

	titleY := l.Header.Min.Y + (cardHeader-CharH)/2 - 3
	DrawText(img, l.Header.Min.X+modalPad, titleY, i18n.T(i18n.ModalTitle), ColorText)

	if host := state.SelectedHost(); host != nil {
		name := host.DisplayName()
		DrawText(img, l.Header.Max.X-modalPad-TextWidth(name, false), titleY, name, ColorAccent)
	}

	// ── Fields ───────────────────────────────────────────────────────────────
	labels := [3]string{
		i18n.T(i18n.LabelUsername),
		i18n.T(i18n.LabelPassword),
		i18n.T(i18n.LabelDomain),
	}
	for i, label := range labels {
		f := l.Fields[i]
		DrawText(img, l.LabelX, f.Min.Y+(modalFieldH-CharH)/2, label, ColorDim)

		text := state.Modal.Fields[i]
		if i == 1 {
			text = maskStr(text)
		}
		DrawInputField(img, f, text, state.Modal.FocusIdx == i)
	}

	if state.Modal.Error != "" {
		DrawText(img, l.LabelX, l.ErrorY, state.Modal.Error, ColorError)
	}

	// ── Buttons ──────────────────────────────────────────────────────────────
	drawButton(img, l.Connect, i18n.T(i18n.ButtonConnect), state.Modal.FocusIdx == 3, true)
	drawButton(img, l.Cancel, i18n.T(i18n.ButtonCancel), state.Modal.FocusIdx == 4, false)
}

// drawButton renders one dialog button. The primary button is filled; the
// secondary one is outlined, so the default action reads at a glance.
func drawButton(img *image.RGBA, r image.Rectangle, label string, focused, primary bool) {
	switch {
	case primary:
		FillRect(img, r, ColorAccent)
	default:
		FillRect(img, r, ColorPanel)
		DrawBorder(img, r, ColorBorder)
	}

	if focused {
		// A focus ring rather than a colour swap, so the primary button does not
		// change meaning when it gains focus.
		DrawBorder(img, r, ColorFocus)
		DrawBorder(img, r.Inset(1), ColorFocus)
	}

	textColor := ColorText
	if !primary && !focused {
		textColor = ColorMuted
	}
	DrawText(img,
		r.Min.X+(r.Dx()-TextWidth(label, false))/2,
		r.Min.Y+(r.Dy()-CharH)/2,
		label, textColor)
}

func maskStr(s string) string {
	out := make([]rune, len([]rune(s)))
	for i := range out {
		out[i] = '•'
	}
	return string(out)
}
