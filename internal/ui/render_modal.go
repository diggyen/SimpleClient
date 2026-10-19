package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/autoconnect"
	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	modalW      = 480
	modalPad    = 18
	modalFieldH = 26
	modalFieldG = 12 // gap between fields
	modalLabelW = 88 // label column; the widest label is Turkish "Etki alanı:"
	modalHintH  = CharH + 8
	modalHintG  = 6 // between the two toggles under the fields
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
	Fields  []image.Rectangle // as many as the dialog shows
	LabelX  int
	Domain  image.Rectangle // the "F6 Domain" toggle under the fields
	Auto    image.Rectangle // the "F10 At startup" toggle under it
	ErrorY  int
	Connect image.Rectangle
	Cancel  image.Rectangle
}

// layoutModal lays the dialog out with the given number of text fields: two,
// or three with the domain shown.
func layoutModal(screen image.Rectangle, fields int) modalLayout {
	h := cardHeader + modalPad +
		fields*modalFieldH + (fields-1)*modalFieldG +
		modalFieldG + 2*modalHintH + modalHintG +
		modalErrH + modalPad + modalBtnH + modalPad

	box := image.Rect(0, 0, modalW, h).
		Add(image.Pt((screen.Dx()-modalW)/2, (screen.Dy()-h)/2))

	l := modalLayout{
		Box:    box,
		Header: image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader),
		LabelX: box.Min.X + modalPad,
		Fields: make([]image.Rectangle, fields),
	}

	inputX := box.Min.X + modalPad + modalLabelW
	inputW := box.Max.X - modalPad - inputX
	y := l.Header.Max.Y + modalPad
	for i := range l.Fields {
		l.Fields[i] = image.Rect(inputX, y, inputX+inputW, y+modalFieldH)
		y += modalFieldH + modalFieldG
	}

	l.Domain = image.Rect(inputX, y, inputX+domainToggleWidth(), y+modalHintH)
	y += modalHintH + modalHintG
	l.Auto = image.Rect(inputX, y, inputX+inputW, y+modalHintH)
	y += modalHintH

	l.ErrorY = y + 3

	btnY := box.Max.Y - modalPad - modalBtnH
	cx := box.Min.X + box.Dx()/2
	l.Connect = image.Rect(cx-modalBtnW-modalBtnGap/2, btnY, cx-modalBtnGap/2, btnY+modalBtnH)
	l.Cancel = image.Rect(cx+modalBtnGap/2, btnY, cx+modalBtnW+modalBtnGap/2, btnY+modalBtnH)
	return l
}

// domainToggleWidth is the width of the "F6 Domain" toggle: the keycap, the
// gap DrawKeycap's callers leave, and the label.
func domainToggleWidth() int {
	return TextWidth("F6", false) + 10 + 7 + TextWidth(i18n.T(i18n.HintDomain), false)
}

// autoLabel names a startup choice for the dialog's F10 toggle.
func autoLabel(m autoconnect.Mode) string {
	switch m {
	case autoconnect.Dialog:
		return i18n.T(i18n.HintAutoDialog)
	case autoconnect.Logon:
		return i18n.T(i18n.HintAutoLogon)
	}
	return i18n.T(i18n.HintAutoOff)
}

// renderModal draws the credential entry modal over the current screen.
func renderModal(img *image.RGBA, state *UIState) {
	m := &state.Modal
	l := layoutModal(img.Bounds(), m.fieldCount())

	// Dim everything behind it so the dialog is unmistakably the focus.
	FillRectBlend(img, img.Bounds(), ColorOverlay)

	DrawPanel(img, l.Box, ColorPanel, ColorAccent)

	// ── Header: title, and which host is about to be connected to ────────────
	strip := image.Rect(l.Header.Min.X+1, l.Header.Min.Y+1, l.Header.Max.X-1, l.Header.Max.Y)
	FillNotchedTop(img, strip, ColorCardHeader)
	DrawHLine(img, strip.Min.X+2, strip.Max.X-2, strip.Min.Y, ColorPanelEdge)
	DrawHLine(img, strip.Min.X, strip.Max.X, strip.Max.Y-1, ColorBorder)

	titleY := l.Header.Min.Y + (cardHeader-CharH)/2
	FillRect(img, image.Rect(
		l.Header.Min.X+modalPad, titleY+1,
		l.Header.Min.X+modalPad+3, titleY+CharH-1,
	), ColorAccent)
	DrawText(img, l.Header.Min.X+modalPad+11, titleY, i18n.T(i18n.ModalTitle), ColorText)

	// The name the scan found for the address in the server field, which the
	// field itself cannot show.
	if host := state.SelectedHost(); host != nil && host.Hostname != "" &&
		m.Fields[fieldServer] == host.Address() {
		name := host.Hostname
		DrawText(img, l.Header.Max.X-modalPad-TextWidth(name, false), titleY, name, ColorAccent)
	}

	// ── Fields ───────────────────────────────────────────────────────────────
	labels := [...]string{
		fieldServer:   i18n.T(i18n.LabelServer),
		fieldUser:     i18n.T(i18n.LabelUsername),
		fieldPassword: i18n.T(i18n.LabelPassword),
		fieldDomain:   i18n.T(i18n.LabelDomain),
	}
	for i, f := range l.Fields {
		DrawText(img, l.LabelX, f.Min.Y+(modalFieldH-CharH)/2, labels[i], ColorDim)

		text := m.Fields[i]
		if i == fieldPassword {
			text = maskStr(text)
		}
		DrawInputField(img, f, text, m.FocusIdx == i)
	}

	// The domain toggle: a keycap reads as "press this", and it is the same
	// control the mouse clicks.
	x := l.Domain.Min.X + DrawKeycap(img, l.Domain.Min.X, l.Domain.Min.Y, "F6") + 7
	DrawText(img, x, l.Domain.Min.Y+4, i18n.T(i18n.HintDomain), ColorDim)

	// What the kiosk does at startup, lit once it is more than the list.
	x = l.Auto.Min.X + DrawKeycap(img, l.Auto.Min.X, l.Auto.Min.Y, "F10") + 7
	label, c := autoLabel(m.Auto), ColorDim
	if m.Auto != autoconnect.Off {
		c = ColorText
	}
	DrawText(img, x, l.Auto.Min.Y+4, label, c)

	if m.Error != "" {
		DrawText(img, l.LabelX, l.ErrorY, m.Error, ColorError)
	}

	// ── Buttons ──────────────────────────────────────────────────────────────
	drawButton(img, l.Connect, i18n.T(i18n.ButtonConnect), m.FocusIdx == m.focusConnect(), true)
	drawButton(img, l.Cancel, i18n.T(i18n.ButtonCancel), m.FocusIdx == m.focusCancel(), false)
}

// drawButton renders one dialog button. The primary button is filled; the
// secondary one is outlined, so the default action reads at a glance.
func drawButton(img *image.RGBA, r image.Rectangle, label string, focused, primary bool) {
	if focused {
		// A ring outside the button rather than a colour swap inside it, so the
		// primary button does not change meaning when it gains focus.
		ring := r.Inset(-3)
		FillNotched(img, ring, ColorFocus)
		FillNotched(img, ring.Inset(2), ColorPanel)
	}

	if primary {
		FillNotched(img, r, ColorAccent)
		// The lit top edge is what turns a coloured rectangle into a key that
		// looks pressable, and it is the only thing distinguishing the primary
		// button from the error banner, which is the same family of red.
		DrawHLine(img, r.Min.X+2, r.Max.X-2, r.Min.Y, ColorText)
	} else {
		FillNotched(img, r, ColorBorder)
		FillNotched(img, r.Inset(1), ColorPanel)
		DrawHLine(img, r.Min.X+2, r.Max.X-2, r.Min.Y+1, ColorPanelEdge)
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
