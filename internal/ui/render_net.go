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
	StatusY int
	MsgY    int
	Save    image.Rectangle
	Cancel  image.Rectangle
}

// layoutNet lays the dialog out with the address fields when static is set,
// and without them for DHCP.
func layoutNet(screen image.Rectangle, static bool) netLayout {
	fields := 0
	if static {
		fields = netFields
	}
	h := cardHeader + modalPad +
		netChipH + modalFieldG +
		fields*(modalFieldH+modalFieldG) +
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
	l := layoutNet(img.Bounds(), n.Static)

	FillRectBlend(img, img.Bounds(), ColorOverlay)
	DrawPanel(img, l.Box, ColorPanel, ColorAccent)

	strip := image.Rect(l.Header.Min.X+1, l.Header.Min.Y+1, l.Header.Max.X-1, l.Header.Max.Y)
	FillNotchedTop(img, strip, ColorCardHeader)
	DrawHLine(img, strip.Min.X+2, strip.Max.X-2, strip.Min.Y, ColorPanelEdge)
	DrawHLine(img, strip.Min.X, strip.Max.X, strip.Max.Y-1, ColorBorder)
	titleY := l.Header.Min.Y + (cardHeader-CharH)/2
	FillRect(img, image.Rect(l.Header.Min.X+modalPad, titleY+1, l.Header.Min.X+modalPad+3, titleY+CharH-1), ColorAccent)
	DrawText(img, l.Header.Min.X+modalPad+11, titleY, i18n.T(i18n.NetTitle), ColorText)
	if n.Iface != "" {
		DrawText(img, l.Header.Max.X-modalPad-TextWidth(n.Iface, false), titleY, n.Iface, ColorAccent)
	}

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

	// What the interface has right now.
	st := n.Status
	line := i18n.Tf(i18n.NetNoCable, st.Interface)
	if st.Carrier {
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
