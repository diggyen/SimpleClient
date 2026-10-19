package ui

import (
	"image"
	"image/color"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/wifi"
)

const (
	wifiW      = 600
	wifiRows   = 7 // the list's rows, the hidden network's among them
	wifiRowH   = 30
	wifiLabelW = 110 // Turkish "Güvenlik:" is the widest
)

// wifiLayout places the wireless dialog; drawing and mouse hit-testing share
// it. The box is the same size with the list and with the prompt in it.
type wifiLayout struct {
	Box     image.Rectangle
	Header  image.Rectangle
	List    image.Rectangle
	StatusY int
	MsgY    int
	HintsY  int

	// The prompt.
	LabelX  int
	NameY   int // a network from the list: its name, as text
	Items   map[wifiItem]image.Rectangle
	Chips   []image.Rectangle // a hidden network's security, as hiddenSecurity
	PromptY int               // the prompt's message, under the fields
	Join    image.Rectangle
	Cancel  image.Rectangle
}

// layoutWifi lays the dialog out for w: its list, or the fields its prompt
// asks for.
func layoutWifi(screen image.Rectangle, w *WifiState) wifiLayout {
	h := cardHeader + modalPad + wifiRows*wifiRowH + 8 + 2*(CharH+10) + modalPad + modalHintH + modalPad
	box := image.Rect(0, 0, wifiW, h).
		Add(image.Pt((screen.Dx()-wifiW)/2, (screen.Dy()-h)/2))
	l := wifiLayout{
		Box:    box,
		Header: image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader),
		LabelX: box.Min.X + modalPad,
		Items:  map[wifiItem]image.Rectangle{},
	}
	top := l.Header.Max.Y + modalPad
	l.List = image.Rect(box.Min.X+modalPad/2, top, box.Max.X-modalPad/2, top+wifiRows*wifiRowH)
	l.StatusY = l.List.Max.Y + 12
	l.MsgY = l.StatusY + CharH + 10
	l.HintsY = box.Max.Y - modalPad - modalHintH

	inputX := box.Min.X + modalPad + wifiLabelW
	inputW := box.Max.X - modalPad - inputX
	y := top
	if !w.Hidden {
		l.NameY = y + (modalFieldH-CharH)/2
		y += modalFieldH + modalFieldG
	}
	for _, it := range w.items() {
		if it == itemJoin || it == itemCancel {
			continue
		}
		l.Items[it] = image.Rect(inputX, y, inputX+inputW, y+modalFieldH)
		if it == itemSecurity {
			chipW := (inputW - 2*modalFieldG) / len(hiddenSecurity)
			for i := range hiddenSecurity {
				x := inputX + i*(chipW+modalFieldG)
				l.Chips = append(l.Chips, image.Rect(x, y, x+chipW, y+modalFieldH))
			}
		}
		y += modalFieldH + modalFieldG
	}
	l.PromptY = y + 4

	btnY := box.Max.Y - modalPad - modalBtnH
	cx := box.Min.X + box.Dx()/2
	l.Join = image.Rect(cx-modalBtnW-modalBtnGap/2, btnY, cx-modalBtnGap/2, btnY+modalBtnH)
	l.Cancel = image.Rect(cx+modalBtnGap/2, btnY, cx+modalBtnW+modalBtnGap/2, btnY+modalBtnH)
	return l
}

// row is the screen rectangle of the i'th row shown.
func (l wifiLayout) row(i int) image.Rectangle {
	y := l.List.Min.Y + i*wifiRowH
	return image.Rect(l.List.Min.X, y, l.List.Max.X, y+wifiRowH)
}

// rowAt is the list index under (x, y), or -1.
func (l wifiLayout) rowAt(w *WifiState, x, y int) int {
	if !image.Pt(x, y).In(l.List) {
		return -1
	}
	i := w.Top + (y-l.List.Min.Y)/wifiRowH
	if i > len(w.Networks) {
		return -1
	}
	return i
}

// renderWifi draws the wireless dialog over the current screen.
func renderWifi(img *image.RGBA, state *UIState) {
	w := &state.Wifi
	l := layoutWifi(img.Bounds(), w)

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)
	drawDialogHeader(img, l.Header, i18n.T(i18n.WifiTitle), w.Iface)

	if w.Prompt {
		renderWifiPrompt(img, l, w)
		return
	}
	renderWifiRows(img, l, w)

	line, c := w.statusLine()
	DrawText(img, l.LabelX, l.StatusY, line, c)
	if w.Msg != "" {
		c := ColorText
		if w.Err {
			c = ColorError
		}
		DrawText(img, l.LabelX, l.MsgY, w.Msg, c)
	}

	hints := [][2]string{
		{"↑↓", i18n.T(i18n.HintSelect)},
		{"Enter", i18n.T(i18n.HintConnect)},
		{"F5", i18n.T(i18n.HintRefresh)},
	}
	if w.Kept.SSID != "" {
		hints = append(hints, [2]string{"Del", i18n.T(i18n.HintForget)})
	}
	hints = append(hints, [2]string{"Esc", i18n.T(i18n.HintBack)})
	x := l.Box.Min.X + (l.Box.Dx()-keyHintsWidth(hints))/2
	for _, h := range hints {
		x += DrawKeycap(img, x, l.HintsY, h[0]) + 7
		DrawText(img, x, l.HintsY+4, h[1], ColorDim)
		x += TextWidth(h[1], false) + 22
	}
}

// renderWifiRows draws the networks shown, and the hidden network's row
// after them.
func renderWifiRows(img *image.RGBA, l wifiLayout, w *WifiState) {
	for i := 0; i < wifiRows; i++ {
		idx := w.Top + i
		if idx > len(w.Networks) {
			break
		}
		row := l.row(i)
		selected := idx == w.Sel
		switch {
		case selected:
			FillRect(img, row, ColorSelected)
			FillRect(img, image.Rect(row.Min.X, row.Min.Y, row.Min.X+3, row.Max.Y), ColorAccent)
		case idx%2 == 1:
			FillRect(img, row, ColorRowHover)
		}
		textY := row.Min.Y + (wifiRowH-CharH)/2
		markerX := row.Min.X + 10
		if selected {
			DrawChevron(img, markerX, textY+3, ColorAccent)
		}
		barsX := markerX + 12
		nameX := barsX + SignalWidth + 10
		nameColor := ColorMuted
		if selected {
			nameColor = ColorText
		}

		if idx == len(w.Networks) {
			DrawText(img, nameX, textY, i18n.T(i18n.WifiHidden), nameColor)
			continue
		}
		n := w.Networks[idx]
		DrawSignal(img, image.Rect(barsX, textY, barsX+SignalWidth, textY+CharH-1), n.Bars(), signalColor(n.Bars()))

		// From the right: the security, and the mark of the network the
		// kiosk is on, joining or keeps.
		right := row.Max.X - 12
		sec := securityLabel(n.Security)
		right -= TextWidth(sec, false)
		DrawText(img, right, textY, sec, ColorDim)
		if mark, c := w.mark(n.SSID); mark != "" {
			right -= 16 + TextWidth(mark, false)
			DrawText(img, right, textY, mark, c)
		}
		DrawText(img, nameX, textY, fitText(n.SSID, right-16-nameX), nameColor)
	}

	if len(w.Networks) == 0 {
		msg := i18n.T(i18n.WifiNone)
		switch {
		case w.Down:
			msg = ""
		case time.Since(w.ScanAt) < wifiScanShown:
			msg = i18n.T(i18n.WifiScanning)
		}
		y := l.List.Min.Y + wifiRowH*3 + (wifiRowH-CharH)/2
		DrawText(img, l.List.Min.X+(l.List.Dx()-TextWidth(msg, false))/2, y, msg, ColorDim)
	}

	// The scroll bar, when there are more rows than fit.
	if total := len(w.Networks) + 1; total > wifiRows {
		track := image.Rect(l.List.Max.X-4, l.List.Min.Y, l.List.Max.X-1, l.List.Max.Y)
		FillRect(img, track, ColorTrack)
		h := max(track.Dy()*wifiRows/total, 12)
		y := track.Min.Y + (track.Dy()-h)*w.Top/(total-wifiRows)
		FillRect(img, image.Rect(track.Min.X, y, track.Max.X, y+h), ColorScrollBar)
	}
}

// renderWifiPrompt draws what joining the chosen network takes.
func renderWifiPrompt(img *image.RGBA, l wifiLayout, w *WifiState) {
	if !w.Hidden {
		DrawText(img, l.LabelX, l.NameY, i18n.T(i18n.WifiNetwork), ColorDim)
		x := l.LabelX + wifiLabelW
		sec := securityLabel(w.Net.Security)
		name := fitText(w.Net.SSID, l.Box.Max.X-modalPad-x-TextWidth(sec, false)-16)
		DrawText(img, x, l.NameY, name, ColorText)
		DrawText(img, x+TextWidth(name, false)+16, l.NameY, sec, ColorDim)
	}
	labels := map[wifiItem]string{
		itemName:     i18n.T(i18n.WifiName),
		itemSecurity: i18n.T(i18n.WifiSecurity),
		itemUser:     i18n.T(i18n.LabelUsername),
		itemPassword: i18n.T(i18n.LabelPassword),
	}
	for it, r := range l.Items {
		DrawText(img, l.LabelX, r.Min.Y+(modalFieldH-CharH)/2, labels[it], ColorDim)
		switch it {
		case itemSecurity:
			for i, s := range hiddenSecurity {
				drawChoice(img, l.Chips[i], securityLabel(s), w.Net.Security == s, w.Focus == itemSecurity)
			}
		case itemPassword:
			DrawInputField(img, r, maskStr(w.Fields[it]), w.Focus == it)
		default:
			DrawInputField(img, r, w.Fields[it], w.Focus == it)
		}
	}
	if w.Msg != "" {
		c := ColorText
		if w.Err {
			c = ColorError
		}
		DrawText(img, l.LabelX, l.PromptY, w.Msg, c)
	}
	drawButton(img, l.Join, i18n.T(i18n.ButtonConnect), w.Focus == itemJoin, true)
	drawButton(img, l.Cancel, i18n.T(i18n.ButtonCancel), w.Focus == itemCancel, false)
}

// drawDialogHeader draws a dialog's title strip, with a note at its right end.
func drawDialogHeader(img *image.RGBA, header image.Rectangle, title, note string) {
	strip := image.Rect(header.Min.X+1, header.Min.Y+1, header.Max.X-1, header.Max.Y)
	FillNotchedTop(img, strip, ColorCardHeader)
	DrawHLine(img, strip.Min.X+2, strip.Max.X-2, strip.Min.Y, ColorPanelEdge)
	DrawHLine(img, strip.Min.X, strip.Max.X, strip.Max.Y-1, ColorBorder)
	titleY := header.Min.Y + (cardHeader-CharH)/2
	FillRect(img, image.Rect(header.Min.X+modalPad, titleY+1, header.Min.X+modalPad+3, titleY+CharH-1), ColorAccent)
	DrawText(img, header.Min.X+modalPad+11, titleY, title, ColorText)
	if note != "" {
		DrawText(img, header.Max.X-modalPad-TextWidth(note, false), titleY, note, ColorAccent)
	}
}

// statusLine says how the interface stands: on which network, with which
// address; joining one; or on none, and which it keeps.
func (w *WifiState) statusLine() (string, color.RGBA) {
	switch {
	case w.Down:
		return i18n.T(i18n.WifiDown), ColorError
	case w.Status.Joined():
		addr := w.Address
		if addr == "" {
			addr = i18n.T(i18n.WifiNoAddress)
		}
		return i18n.Tf(i18n.WifiStatusOn, w.Status.SSID) + buildSep + addr, ColorText
	case w.Joining.SSID != "":
		return i18n.Tf(i18n.WifiJoining, w.Joining.SSID), ColorMuted
	case w.Kept.SSID != "" && w.Status.Busy():
		return i18n.Tf(i18n.WifiJoining, w.Kept.SSID), ColorMuted
	case w.Kept.SSID != "":
		return i18n.T(i18n.WifiStatusOff) + buildSep + i18n.Tf(i18n.WifiStatusKept, w.Kept.SSID), ColorDim
	}
	return i18n.T(i18n.WifiStatusOff), ColorDim
}

// summary is the network settings' wireless row: the network the kiosk is
// on, joining, or keeps, and how it stands with it.
func (w *WifiState) summary() string {
	switch {
	case w.Status.Joined():
		return i18n.Tf(i18n.NetWifiOn, w.Status.SSID)
	case w.Joining.SSID != "":
		return i18n.Tf(i18n.NetWifiJoining, w.Joining.SSID)
	case w.Kept.SSID != "" && w.Status.Busy():
		return i18n.Tf(i18n.NetWifiJoining, w.Kept.SSID)
	case w.Kept.SSID != "":
		return i18n.Tf(i18n.NetWifiOff, w.Kept.SSID)
	}
	return i18n.T(i18n.NetWifiChoose)
}

// mark is what the list says of the network named ssid: that the kiosk is on
// it, joining it, or keeps it.
func (w *WifiState) mark(ssid string) (string, color.RGBA) {
	switch {
	case w.on(ssid):
		return i18n.T(i18n.WifiMarkOn), ColorSuccess
	case ssid == w.Joining.SSID:
		return i18n.T(i18n.WifiMarkJoining), ColorMuted
	case ssid == w.Kept.SSID:
		return i18n.T(i18n.WifiMarkKept), ColorDim
	}
	return "", ColorDim
}

// securityLabel names how a network lets clients in.
func securityLabel(s wifi.Security) string {
	switch s {
	case wifi.PSK:
		return "WPA2"
	case wifi.SAE:
		return "WPA3"
	case wifi.EAP:
		return i18n.T(i18n.WifiEnterprise)
	case wifi.WEP:
		return "WEP"
	}
	return i18n.T(i18n.WifiOpen)
}

// signalColor colours a network's bars as the host list colours latency.
func signalColor(bars int) color.RGBA {
	switch {
	case bars >= 3:
		return ColorSuccess
	case bars == 2:
		return ColorWarning
	}
	return ColorError
}

// fitText shortens s with an ellipsis to fit in width pixels.
func fitText(s string, width int) string {
	if TextWidth(s, false) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && TextWidth(string(r)+"...", false) > width {
		r = r[:len(r)-1]
	}
	return string(r) + "..."
}
