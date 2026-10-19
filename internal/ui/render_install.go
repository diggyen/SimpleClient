package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	installW    = 620
	installPad  = 20
	installRowH = 30
	installRows = 6 // disks shown at once; a mini PC rarely has more than two
)

// renderInstall draws the install-to-disk dialog over the server list.
func renderInstall(img *image.RGBA, state *UIState) {
	in := &state.Install
	screen := img.Bounds()

	h := cardHeader + installPad + CharH + 14 + installRows*installRowH + installPad + CharH + 8 + installPad
	box := image.Rect(0, 0, installW, h).
		Add(image.Pt(screen.Min.X+(screen.Dx()-installW)/2, screen.Min.Y+(screen.Dy()-h)/2))

	FillRectBlend(img, screen, ColorOverlay)
	DrawPanel(img, box, ColorPanel, ColorAccent)

	header := image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader)
	strip := image.Rect(header.Min.X+1, header.Min.Y+1, header.Max.X-1, header.Max.Y)
	FillNotchedTop(img, strip, ColorCardHeader)
	DrawHLine(img, strip.Min.X+2, strip.Max.X-2, strip.Min.Y, ColorPanelEdge)
	DrawHLine(img, strip.Min.X, strip.Max.X, strip.Max.Y-1, ColorBorder)
	titleY := header.Min.Y + (cardHeader-CharH)/2
	FillRect(img, image.Rect(
		header.Min.X+installPad, titleY+1,
		header.Min.X+installPad+3, titleY+CharH-1,
	), ColorAccent)
	DrawText(img, header.Min.X+installPad+11, titleY, i18n.T(i18n.InstallTitle), ColorText)

	x := box.Min.X + installPad
	y := header.Max.Y + installPad
	w := box.Dx() - 2*installPad
	footY := box.Max.Y - installPad - CharH - 8

	switch in.Phase {
	case InstallPick:
		if len(in.Disks) == 0 {
			drawWrapped(img, x, y, w, i18n.T(i18n.InstallNoDisk), ColorWarning)
			drawInstallKeys(img, x, footY, [][2]string{{"Esc", i18n.T(i18n.ButtonCancel)}})
			return
		}
		DrawText(img, x, y, i18n.T(i18n.InstallPick), ColorDim)
		y += CharH + 14
		first := 0
		if in.Sel >= installRows {
			first = in.Sel - installRows + 1
		}
		for i := first; i < len(in.Disks) && i < first+installRows; i++ {
			row := image.Rect(x, y, x+w, y+installRowH-4)
			if i == in.Sel {
				FillNotched(img, row, ColorSelected)
				FillRect(img, image.Rect(row.Min.X+2, row.Min.Y+2, row.Min.X+5, row.Max.Y-2), ColorAccent)
			}
			DrawText(img, row.Min.X+14, row.Min.Y+(row.Dy()-CharH)/2, in.Disks[i].Label(), ColorText)
			y += installRowH
		}
		drawInstallKeys(img, x, footY, [][2]string{
			{"↑↓", i18n.T(i18n.HintSelect)},
			{"Enter", i18n.T(i18n.HintInstall)},
			{"Esc", i18n.T(i18n.ButtonCancel)},
		})

	case InstallConfirm:
		d := in.Disks[in.Sel]
		y = drawWrapped(img, x, y, w, fmt.Sprintf(i18n.T(i18n.InstallErase), d.Label()), ColorError)
		y += 12
		DrawText(img, x, y, fmt.Sprintf(i18n.T(i18n.InstallTable), strings.ToUpper(in.Table)), ColorMuted)
		y += CharH + 28
		drawWrapped(img, x, y, w, fmt.Sprintf(i18n.T(i18n.InstallKeys), i18n.T(i18n.InstallLetter)), ColorText)

	case InstallRunning:
		DrawText(img, x, y, installStepText(in.StepName), ColorText)
		y += CharH + 18
		done := 0.0
		if in.Total > 0 && in.Step > 0 {
			done = float64(in.Step-1) / float64(in.Total)
		}
		DrawProgressBar(img, image.Rect(x, y, x+w, y+10), done, ColorAccent, ColorTrack)

	case InstallDone:
		drawWrapped(img, x, y, w, i18n.T(i18n.InstallDone), ColorSuccess)
		drawInstallKeys(img, x, footY, [][2]string{{"Enter", ""}})

	case InstallFailed:
		drawWrapped(img, x, y, w, fmt.Sprintf(i18n.T(i18n.InstallFailed), in.Err), ColorError)
		drawInstallKeys(img, x, footY, [][2]string{{"Enter", ""}})
	}
}

// drawWrapped draws text wrapped to width and returns the y below it.
func drawWrapped(img *image.RGBA, x, y, width int, text string, c color.RGBA) int {
	for _, line := range wrapText(text, width) {
		DrawText(img, x, y, line, c)
		y += CharH + 6
	}
	return y
}

// drawInstallKeys draws a row of key caps with what each does.
func drawInstallKeys(img *image.RGBA, x, y int, keys [][2]string) {
	for _, k := range keys {
		x += DrawKeycap(img, x, y, k[0]) + 7
		if k[1] != "" {
			DrawText(img, x, y+4, k[1], ColorDim)
			x += TextWidth(k[1], false)
		}
		x += 22
	}
}
