package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/display"
	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	displayW     = 560
	displayRowH  = 26
	displayRows  = 8 // resolutions shown at once
	displayLineH = CharH + 6
	displayMaxTx = 7 // lines of text above the list at most
)

// displayLayout places the resolution dialog; drawing and the mouse share it.
type displayLayout struct {
	Box    image.Rectangle
	Header image.Rectangle
	X, W   int // the text column
	TextY  int
	Lines  []string // the text above the list, or the question, wrapped
	// The list: the rows shown, the first of them the choice First.
	Rows  []image.Rectangle
	First int
	KeysY int
	// The question's buttons, Keep then Back.
	Buttons []image.Rectangle
}

func layoutDisplay(screen image.Rectangle, state *UIState) displayLayout {
	d := &state.Display
	w := displayW
	if screen.Dx()-2*padding < w {
		w = screen.Dx() - 2*padding
	}
	l := displayLayout{W: w - 2*modalPad}
	cols := l.W / CharW

	var text string
	switch {
	case d.Going && d.Confirm:
		text = i18n.T(i18n.DisplayReverting)
	case d.Going:
		text = i18n.Tf(i18n.DisplayRestarting, displayLabel(d.Choices[d.Sel]))
	case d.Confirm:
		text = i18n.Tf(i18n.DisplayConfirmQ, displayLabel(d.Mode), d.Left)
	default:
		text = i18n.Tf(i18n.DisplayNow, d.Now)
		if display.Blind(d.Outputs) {
			text += " " + i18n.T(i18n.DisplayBlind)
		}
		if state.DriverOff {
			text += " " + i18n.T(i18n.DisplaySafe)
		}
	}
	l.Lines = wrapText(text, cols)
	if !d.Going && !d.Confirm && d.Err != "" {
		l.Lines = append(l.Lines, wrapText(i18n.Tf(i18n.DisplayFailed, d.Err), cols)...)
	}
	if len(l.Lines) > displayMaxTx {
		l.Lines = l.Lines[:displayMaxTx]
	}
	textH := len(l.Lines) * displayLineH

	h := cardHeader + modalPad + textH + modalPad
	list := !d.Going && !d.Confirm
	rows := 0
	if list {
		rows = min(displayRows, len(d.Choices))
		h += rows*displayRowH + modalPad + CharH + 8 + modalPad
	} else if d.Confirm && !d.Going {
		h += modalBtnH + modalPad
	}
	box := image.Rect(0, 0, w, h).
		Add(image.Pt(screen.Min.X+(screen.Dx()-w)/2, screen.Min.Y+(screen.Dy()-h)/2))
	l.Box = box
	l.Header = image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader)
	l.X = box.Min.X + modalPad
	l.TextY = l.Header.Max.Y + modalPad

	y := l.TextY + textH + modalPad
	if list {
		if d.Sel >= rows {
			l.First = d.Sel - rows + 1
		}
		for i := 0; i < rows; i++ {
			l.Rows = append(l.Rows, image.Rect(l.X, y, l.X+l.W, y+displayRowH-4))
			y += displayRowH
		}
		l.KeysY = box.Max.Y - modalPad - CharH - 8
	}
	if d.Confirm && !d.Going {
		x := box.Min.X + (box.Dx()-2*modalBtnW-modalBtnGap)/2
		for range 2 {
			l.Buttons = append(l.Buttons, image.Rect(x, y, x+modalBtnW, y+modalBtnH))
			x += modalBtnW + modalBtnGap
		}
	}
	return l
}

// renderDisplay draws the dialog over the server list.
func renderDisplay(img *image.RGBA, state *UIState) {
	d := &state.Display
	l := layoutDisplay(img.Bounds(), state)

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)
	title := i18n.T(i18n.DisplayTitle)
	if d.Confirm {
		title = i18n.T(i18n.DisplayConfirmTitle)
	}
	drawDialogHeader(img, l.Header, title, "")

	y := l.TextY
	for _, line := range l.Lines {
		DrawText(img, l.X, y, line, ColorText)
		y += displayLineH
	}

	unlisted := len(d.Outputs) > 0 && !display.Blind(d.Outputs)
	for i, row := range l.Rows {
		c := l.First + i
		if c == d.Sel {
			FillNotched(img, row, ColorSelected)
			FillRect(img, image.Rect(row.Min.X+2, row.Min.Y+2, row.Min.X+5, row.Max.Y-2), ColorAccent)
		}
		label := displayLabel(d.Choices[c])
		ty := row.Min.Y + (row.Dy()-CharH)/2
		DrawText(img, row.Min.X+14, ty, label, ColorText)
		// What sets a resolution apart: the one kept, and the ones a display
		// that says what it is did not list, which it may not be able to
		// show. One that says nothing lists none of them.
		var notes []string
		if d.Choices[c] == state.Boot.Video {
			notes = append(notes, i18n.T(i18n.DisplaySaved))
		}
		if m, ok := display.Parse(d.Choices[c]); ok && unlisted && !display.Reported(d.Outputs, m) {
			notes = append(notes, i18n.T(i18n.DisplayUnlisted))
		}
		if len(notes) > 0 {
			note := notes[0]
			for _, n := range notes[1:] {
				note += buildSep + n
			}
			DrawText(img, row.Min.X+14+TextWidth(label, false)+16, ty, note, ColorDim)
		}
	}
	if len(l.Rows) > 0 {
		drawInstallKeys(img, l.X, l.KeysY, [][2]string{
			{"↑↓", i18n.T(i18n.HintSelect)},
			{"Enter", i18n.T(i18n.ButtonApply)},
			{"Esc", i18n.T(i18n.ButtonCancel)},
		})
	}

	labels := []string{i18n.T(i18n.ButtonKeep), i18n.T(i18n.ButtonRevert)}
	for i, b := range l.Buttons {
		drawButton(img, b, labels[i], d.Focus == i, i == displayFocusKeep)
	}
}
