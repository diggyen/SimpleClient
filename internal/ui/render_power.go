package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const powerW = 480

// powerLayout places the restart / switch-off dialog; drawing and mouse
// hit-testing share it.
type powerLayout struct {
	Box     image.Rectangle
	Header  image.Rectangle
	X, W    int // the text column
	TextY   int
	Buttons [powerButtons]image.Rectangle // Restart, Switch off, Cancel
}

func layoutPower(screen image.Rectangle) powerLayout {
	h := cardHeader + modalPad + 2*(CharH+6) + modalPad + modalBtnH + modalPad
	box := image.Rect(0, 0, powerW, h).
		Add(image.Pt(screen.Min.X+(screen.Dx()-powerW)/2, screen.Min.Y+(screen.Dy()-h)/2))

	l := powerLayout{
		Box:    box,
		Header: image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader),
		X:      box.Min.X + modalPad,
		W:      box.Dx() - 2*modalPad,
	}
	l.TextY = l.Header.Max.Y + modalPad

	btnY := box.Max.Y - modalPad - modalBtnH
	x := box.Min.X + (box.Dx()-powerButtons*modalBtnW-(powerButtons-1)*modalBtnGap)/2
	for i := range l.Buttons {
		l.Buttons[i] = image.Rect(x, btnY, x+modalBtnW, btnY+modalBtnH)
		x += modalBtnW + modalBtnGap
	}
	return l
}

// renderPower draws the dialog over the server list.
func renderPower(img *image.RGBA, state *UIState) {
	p := &state.Power
	l := layoutPower(img.Bounds())

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)
	drawDialogHeader(img, l.Header, i18n.T(i18n.PowerTitle), "")

	switch p.Going {
	case powerFocusRestart:
		DrawText(img, l.X, l.TextY, i18n.T(i18n.PowerRestarting), ColorText)
		return
	case powerFocusOff:
		DrawText(img, l.X, l.TextY, i18n.T(i18n.PowerSwitchingOff), ColorText)
		return
	}

	y := l.TextY
	for _, line := range wrapText(i18n.T(i18n.PowerQuestion), l.W/CharW) {
		DrawText(img, l.X, y, line, ColorText)
		y += CharH + 6
	}
	labels := [powerButtons]string{i18n.T(i18n.ButtonRestart), i18n.T(i18n.ButtonSwitchOff), i18n.T(i18n.ButtonCancel)}
	for i, b := range l.Buttons {
		drawButton(img, b, labels[i], p.Focus == i, i == powerFocusRestart)
	}
}
