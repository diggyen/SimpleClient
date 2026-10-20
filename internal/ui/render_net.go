package ui

import (
	"image"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

const (
	netW      = 560
	netLabelW = 140 // Turkish "Tercih edilen DNS:" is the widest
	netChipH  = modalFieldH
)

// netLayout places the network dialog; drawing and mouse hit-testing share it.
type netLayout struct {
	Box     image.Rectangle
	Header  image.Rectangle
	LabelX  int
	DHCP    image.Rectangle // the two choices of the mode row
	Static  image.Rectangle
	Fields  []image.Rectangle // none while DHCP is chosen
	Port    image.Rectangle   // the port the list scans for, either way
	Wifi    image.Rectangle   // the wireless row; empty without it
	StatusY int
	MsgY    int
	Save    image.Rectangle
	Cancel  image.Rectangle
}

// layoutNet lays the dialog out with the address fields when static is set,
// and without them for DHCP; with the wireless row when wireless is set.
func layoutNet(screen image.Rectangle, static, wireless bool) netLayout {
	fields, rows := 0, 0
	if static {
		fields = netFields
	}
	if wireless {
		rows = 1
	}
	h := cardHeader + modalPad +
		netChipH + modalFieldG +
		(fields+1)*(modalFieldH+modalFieldG) +
		rows*(netChipH+modalFieldG) +
		2*(CharH+10) +
		modalPad + modalBtnH + modalPad

	box := image.Rect(0, 0, netW, h).
		Add(image.Pt((screen.Dx()-netW)/2, (screen.Dy()-h)/2))
	l := netLayout{
		Box:    box,
		Header: image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+cardHeader),
		LabelX: box.Min.X + modalPad,
		Fields: make([]image.Rectangle, fields),
	}

	inputX := box.Min.X + modalPad + netLabelW
	inputW := box.Max.X - modalPad - inputX
	y := l.Header.Max.Y + modalPad

	chipW := (inputW - modalFieldG) / 2
	l.DHCP = image.Rect(inputX, y, inputX+chipW, y+netChipH)
	l.Static = image.Rect(inputX+chipW+modalFieldG, y, inputX+inputW, y+netChipH)
	y += netChipH + modalFieldG

	for i := range l.Fields {
		l.Fields[i] = image.Rect(inputX, y, inputX+inputW, y+modalFieldH)
		y += modalFieldH + modalFieldG
	}
	l.Port = image.Rect(inputX, y, inputX+inputW, y+modalFieldH)
	y += modalFieldH + modalFieldG
	if wireless {
		l.Wifi = image.Rect(inputX, y, inputX+inputW, y+netChipH)
		y += netChipH + modalFieldG
	}
	l.StatusY = y + 2
	l.MsgY = l.StatusY + CharH + 10

	btnY := box.Max.Y - modalPad - modalBtnH
	cx := box.Min.X + box.Dx()/2
	l.Save = image.Rect(cx-modalBtnW-modalBtnGap/2, btnY, cx-modalBtnGap/2, btnY+modalBtnH)
	l.Cancel = image.Rect(cx+modalBtnGap/2, btnY, cx+modalBtnW+modalBtnGap/2, btnY+modalBtnH)
	return l
}

// renderNet draws the network settings dialog over the current screen.
func renderNet(img *image.RGBA, state *UIState) {
	n := &state.Net
	l := layoutNet(img.Bounds(), n.Static, n.Wireless != "")

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)
	drawDialogHeader(img, l.Header, i18n.T(i18n.NetTitle), n.Iface)

	// The DHCP/manual switch: two chips, the chosen one filled.
	DrawText(img, l.LabelX, l.DHCP.Min.Y+(netChipH-CharH)/2, i18n.T(i18n.NetMode), ColorDim)
	drawChoice(img, l.DHCP, i18n.T(i18n.NetDHCP), !n.Static, n.Focus == netFocusMode)
	drawChoice(img, l.Static, i18n.T(i18n.NetStatic), n.Static, n.Focus == netFocusMode)

	labels := [netFields]string{
		netAddress: i18n.T(i18n.NetAddress),
		netMask:    i18n.T(i18n.NetMask),
		netGateway: i18n.T(i18n.NetGateway),
		netDNS1:    i18n.T(i18n.NetDNS1),
		netDNS2:    i18n.T(i18n.NetDNS2),
	}
	for i, f := range l.Fields {
		DrawText(img, l.LabelX, f.Min.Y+(modalFieldH-CharH)/2, labels[i], ColorDim)
		DrawInputField(img, f, n.Fields[i], n.Focus == i+1)
	}
	// The port the server list scans for, with DHCP or without.
	DrawText(img, l.LabelX, l.Port.Min.Y+(modalFieldH-CharH)/2, i18n.T(i18n.NetPort), ColorDim)
	DrawInputField(img, l.Port, n.Port, n.Focus == netFocusPort)

	// The wireless network: which, and whether the kiosk is on it. The row
	// opens the wireless dialog.
	if n.Wireless != "" {
		DrawText(img, l.LabelX, l.Wifi.Min.Y+(netChipH-CharH)/2, i18n.T(i18n.NetWireless), ColorDim)
		drawButton(img, l.Wifi, state.Wifi.summary(), n.Focus == netFocusWifi, false)
	}

	// What the interface has right now.
	st := n.Status
	line := i18n.Tf(i18n.NetNoCable, st.Interface)
	switch {
	case wifiIsWireless(st.Interface):
		line = i18n.Tf(i18n.NetIfWireless, st.Interface)
	case st.Carrier:
		line = i18n.Tf(i18n.NetCable, st.Interface)
	}
	addr := st.Address
	if addr == "" {
		addr = i18n.T(i18n.NetNoAddress)
	}
	DrawText(img, l.LabelX, l.StatusY, line+buildSep+addr, ColorDim)

	if n.Msg != "" {
		c := ColorText
		if n.Err {
			c = ColorError
		}
		DrawText(img, l.LabelX, l.MsgY, n.Msg, c)
	}

	drawButton(img, l.Save, i18n.T(i18n.NetSave), n.Focus == netFocusSave, true)
	drawButton(img, l.Cancel, i18n.T(i18n.ButtonCancel), n.Focus == netFocusCancel, false)
}

// drawChoice draws one option of a two-way switch.
func drawChoice(img *image.RGBA, r image.Rectangle, label string, chosen, focused bool) {
	switch {
	case chosen:
		FillNotched(img, r, ColorAccent)
		DrawHLine(img, r.Min.X+2, r.Max.X-2, r.Min.Y, ColorText)
	default:
		FillNotched(img, r, ColorBorder)
		FillNotched(img, r.Inset(1), ColorPanel)
	}
	if focused && chosen {
		DrawBorder(img, r.Inset(-3), ColorFocus)
	}
	c := ColorMuted
	if chosen {
		c = ColorText
	}
	DrawText(img, r.Min.X+(r.Dx()-TextWidth(label, false))/2, r.Min.Y+(r.Dy()-CharH)/2, label, c)
}
