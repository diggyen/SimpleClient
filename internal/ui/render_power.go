package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	powerW      = 480 // three buttons
	powerWide   = 620 // four, with the GPU's driver to try
	powerLineH  = CharH + 6
	powerParaG  = 8 // between the question and the note under it
	powerMaxTxt = 6 // lines of text at most
)

// powerLayout places the restart / switch-off dialog; drawing and mouse
// hit-testing share it.
type powerLayout struct {
	Box     image.Rectangle
	Header  image.Rectangle
	X, W    int // the text column
	TextY   int
	Lines   []string          // the text, wrapped; "" between paragraphs
	Buttons []image.Rectangle // left to right
	Kinds   []int             // what each button does: powerFocusRestart ...
}

func layoutPower(screen image.Rectangle, p *PowerState) powerLayout {
	w := powerW
	if p.Driver {
		w = powerWide
	}
	l := powerLayout{X: modalPad, W: w - 2*modalPad, Kinds: p.buttons()}
	cols := l.W / CharW
	l.Lines = wrapText(i18n.T(i18n.PowerQuestion), cols)
	if p.Driver {
		l.Lines = append(append(l.Lines, ""), wrapText(i18n.T(i18n.PowerDriverNote), cols)...)
	}
	if len(l.Lines) > powerMaxTxt {
		l.Lines = l.Lines[:powerMaxTxt]
	}
	textH := 0
	for _, line := range l.Lines {
		if line == "" {
			textH += powerParaG
		} else {
			textH += powerLineH
		}
	}

	h := cardHeader + modalPad + textH + modalPad + modalBtnH + modalPad
	box := image.Rect(0, 0, w, h).
		Add(image.Pt(screen.Min.X+(screen.Dx()-w)/2, screen.Min.Y+(screen.Dy()-h)/2))
	l.Box = box
	l.Header = image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader)
	l.X = box.Min.X + modalPad
	l.TextY = l.Header.Max.Y + modalPad

	n := len(l.Kinds)
	btnY := box.Max.Y - modalPad - modalBtnH
	x := box.Min.X + (box.Dx()-n*modalBtnW-(n-1)*modalBtnGap)/2
	for range l.Kinds {
		l.Buttons = append(l.Buttons, image.Rect(x, btnY, x+modalBtnW, btnY+modalBtnH))
		x += modalBtnW + modalBtnGap
	}
	return l
}

// renderPower draws the dialog over the server list.
func renderPower(img *image.RGBA, state *UIState) {
	p := &state.Power
	l := layoutPower(img.Bounds(), p)

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)
	drawDialogHeader(img, l.Header, i18n.T(i18n.PowerTitle), "")

	going := ""
	switch p.Going {
	case powerFocusRestart:
		going = i18n.T(i18n.PowerRestarting)
		if p.Why != "" {
			going = p.Why
		}
	case powerFocusOff:
		going = i18n.T(i18n.PowerSwitchingOff)
	case powerFocusDriver:
		going = i18n.T(i18n.PowerDriverTrying)
	}
	if going != "" {
		y := l.TextY
		for _, line := range wrapText(going, l.W/CharW) {
			DrawText(img, l.X, y, line, ColorText)
			y += powerLineH
		}
		return
	}

	y := l.TextY
	for _, line := range l.Lines {
		if line == "" {
			y += powerParaG
			continue
		}
		DrawText(img, l.X, y, line, ColorText)
		y += powerLineH
	}
	labels := map[int]string{
		powerFocusRestart: i18n.T(i18n.ButtonRestart),
		powerFocusOff:     i18n.T(i18n.ButtonSwitchOff),
		powerFocusDriver:  i18n.T(i18n.ButtonDriver),
		powerFocusCancel:  i18n.T(i18n.ButtonCancel),
	}
	for i, b := range l.Buttons {
		k := l.Kinds[i]
		drawButton(img, b, labels[k], p.Focus == k, k == powerFocusRestart)
	}
}
