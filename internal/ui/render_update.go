package ui

import (
	"image"
	"image/color"

	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/version"
)

const (
	updateW         = 560
	updateLineGap   = 10
	updateStatusMax = 3 // lines of status text: a reason can be long
)

// updateLayout places the update dialog; drawing and mouse hit-testing share
// it.
type updateLayout struct {
	Box     image.Rectangle
	Header  image.Rectangle
	X, W    int // the text column
	ValueX  int // where the versions go, after their labels
	ThisY   int
	LatestY int
	StatusY int
	Main    image.Rectangle
	Close   image.Rectangle
}

func layoutUpdate(screen image.Rectangle) updateLayout {
	statusH := updateStatusMax * (CharH + 6)
	h := cardHeader + modalPad +
		2*(CharH+updateLineGap) + 8 +
		statusH +
		modalPad + modalBtnH + modalPad
	box := image.Rect(0, 0, updateW, h).
		Add(image.Pt(screen.Min.X+(screen.Dx()-updateW)/2, screen.Min.Y+(screen.Dy()-h)/2))

	l := updateLayout{
		Box:    box,
		Header: image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader),
		X:      box.Min.X + modalPad,
		W:      box.Dx() - 2*modalPad,
	}
	labelW := max(TextWidth(i18n.T(i18n.UpdateThis), false), TextWidth(i18n.T(i18n.UpdateLatest), false))
	l.ValueX = l.X + labelW + 2*CharW
	l.ThisY = l.Header.Max.Y + modalPad
	l.LatestY = l.ThisY + CharH + updateLineGap
	l.StatusY = l.LatestY + CharH + updateLineGap + 8

	btnY := box.Max.Y - modalPad - modalBtnH
	cx := box.Min.X + box.Dx()/2
	l.Main = image.Rect(cx-modalBtnW-modalBtnGap/2, btnY, cx-modalBtnGap/2, btnY+modalBtnH)
	l.Close = image.Rect(cx+modalBtnGap/2, btnY, cx+modalBtnW+modalBtnGap/2, btnY+modalBtnH)
	return l
}

// renderUpdate draws the update dialog over the server list.
func renderUpdate(img *image.RGBA, state *UIState) {
	u := &state.Update
	l := layoutUpdate(img.Bounds())

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)
	drawDialogHeader(img, l.Header, i18n.T(i18n.UpdateTitle), "")

	DrawText(img, l.X, l.ThisY, i18n.T(i18n.UpdateThis), ColorDim)
	DrawText(img, l.ValueX, l.ThisY, version.Version, ColorText)

	DrawText(img, l.X, l.LatestY, i18n.T(i18n.UpdateLatest), ColorDim)
	latest, c := i18n.T(i18n.UpdateNotChecked), ColorMuted
	switch {
	case u.Phase == UpdateChecking:
		latest = i18n.T(i18n.UpdateChecking)
	case u.Checked:
		latest, c = u.Latest.Tag, ColorText
		if u.Available() {
			c = ColorSuccess
		}
	}
	DrawText(img, l.ValueX, l.LatestY, latest, c)

	switch u.Phase {
	case UpdateRunning:
		frac := 0.0
		if u.Total > 0 {
			frac = float64(u.Done) / float64(u.Total)
		}
		DrawProgressBar(img, image.Rect(l.X, l.StatusY, l.X+l.W, l.StatusY+10), frac, ColorAccent, ColorTrack)
		line := i18n.Tf(i18n.UpdateDownloadingNoSize, u.Latest.Tag, u.Done>>20)
		if u.Total > 0 {
			line = i18n.Tf(i18n.UpdateDownloading, u.Latest.Tag, u.Done>>20, u.Total>>20)
		}
		DrawText(img, l.X, l.StatusY+10+updateLineGap, line, ColorText)
		drawButton(img, l.Close, i18n.T(i18n.ButtonCancel), true, false)

	case UpdateRestart:
		drawStatus(img, l, i18n.Tf(i18n.UpdateRestarting, u.Latest.Tag), ColorSuccess)

	default:
		msg, c := updateStatus(u)
		drawStatus(img, l, msg, c)
		main := i18n.T(i18n.ButtonCheck)
		if u.Available() {
			main = i18n.T(i18n.ButtonUpdate)
		}
		drawButton(img, l.Main, main, u.Focus == updateFocusMain, true)
		drawButton(img, l.Close, i18n.T(i18n.ButtonClose), u.Focus == updateFocusClose, false)
	}
}

// updateStatus is what the dialog says under the versions while it waits for
// a choice: why the last attempt failed, or what the last check found.
func updateStatus(u *UpdateState) (string, color.RGBA) {
	switch {
	case u.Err != "":
		return u.Err, ColorError
	case !u.Checked:
		return "", ColorText
	case u.Available() && u.Latest.Tag == u.Failed:
		return i18n.Tf(i18n.UpdateDidNotStart, u.Latest.Tag), ColorWarning
	case u.Available():
		return i18n.Tf(i18n.UpdateReady, u.Latest.Tag), ColorText
	case u.newer():
		return i18n.Tf(i18n.UpdateNoDisk, u.Latest.Tag), ColorWarning
	}
	return i18n.T(i18n.UpdateUpToDate), ColorSuccess
}

// renderUpdateBanner says under the server list how the last update went,
// below the error banner if there is one, or at the top of the screen when a
// long list leaves no room under it: green when the release is up and
// running, amber when the kiosk went back to this version.
func renderUpdateBanner(img *image.RGBA, l discoveryLayout, state *UIState) {
	msg, ok := updateBanner(&state.Update)
	if msg == "" {
		return
	}
	const lineH, oneLine = CharH + 6, 32
	textX := l.Card.Min.X + cardPad + 13
	lines := wrapText(msg, (l.Card.Max.X-cardPad-textX)/CharW)
	h := oneLine + (len(lines)-1)*lineH
	top := l.Card.Max.Y + 14
	if state.ErrorMsg != "" {
		top += oneLine + 8
	}
	if top+h > l.Screen.Max.Y-hintsBottom-8 {
		top = l.Screen.Min.Y + 16
	}
	banner := image.Rect(l.Card.Min.X, top, l.Card.Max.X, top+h)

	edge, fill, mark := ColorSuccessBorder, ColorSuccessBG, ColorSuccess
	if !ok {
		edge, fill, mark = ColorWarningBorder, ColorWarningBG, ColorWarning
	}
	FillNotched(img, banner, edge)
	FillNotched(img, banner.Inset(1), fill)
	FillRect(img, image.Rect(banner.Min.X+2, banner.Min.Y+2, banner.Min.X+5, banner.Max.Y-2), mark)
	y := banner.Min.Y + (oneLine-CharH)/2
	FillRect(img, image.Rect(banner.Min.X+cardPad, y+1, banner.Min.X+cardPad+5, y+6), mark)
	for _, line := range lines {
		DrawText(img, textX, y, line, ColorText)
		y += lineH
	}
}

// drawStatus draws the dialog's status text, wrapped to the box.
func drawStatus(img *image.RGBA, l updateLayout, msg string, c color.RGBA) {
	lines := wrapText(msg, l.W/CharW)
	if len(lines) > updateStatusMax {
		lines = lines[:updateStatusMax]
	}
	y := l.StatusY
	for _, line := range lines {
		DrawText(img, l.X, y, line, c)
		y += CharH + 6
	}
}
