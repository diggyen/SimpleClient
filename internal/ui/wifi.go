package ui

import (
	"errors"
	"image"
	"log"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/netconf"
	"github.com/diggyen/SimpleClient/internal/wifi"
)

// WifiState is the wireless network dialog, opened from the network settings
// (F8) on a kiosk with a wireless interface: the networks in range, strongest
// first, and the one the kiosk keeps. Choosing a network asks for what it
// takes — a password; a user name and a password on an enterprise network;
// nothing on an open one — and joins it. The network is kept only once the
// kiosk is on it: a mistyped password puts the network before it back,
// rather than leaving the kiosk off the network at the next boot.
type WifiState struct {
	Iface    string
	Networks []wifi.Network
	Sel      int // a network, or len(Networks): the hidden network's row
	Top      int // the first row shown
	Kept     wifi.Config
	Status   wifi.Status
	Address  string    // the interface's IPv4 address, once it has one
	Down     bool      // wpa_supplicant does not answer
	ScanAt   time.Time // when a scan was last asked for

	// The prompt for the network chosen, shown instead of the list while
	// Prompt is set.
	Prompt bool
	Net    wifi.Network // the network asked for; a hidden one's name is typed
	Hidden bool
	Fields [itemPassword + 1]string // by itemName, itemUser and itemPassword
	Focus  wifiItem

	Msg  string // the outcome of the last action
	Err  bool   // whether Msg is an error
	Busy bool   // applying; keys are ignored meanwhile

	// Joining is the network being joined, until the kiosk is on it or it is
	// given up on at JoinBy, when Prev, the network before it, is put back.
	Joining wifi.Config
	JoinBy  time.Time
	Prev    wifi.Config

	polling bool
	gen     int // bumped by every change a poll under way does not know of
}

// What the dialog runs, swapped by tests. A goroutine takes the ones it calls
// as it is started, under the lock, rather than reading them as it runs.
var (
	wifiInterfaces = wifi.Interfaces
	wifiIsWireless = wifi.IsWireless
	wifiScan       = wifi.Scan
	wifiResults    = wifi.Results
	wifiCurrent    = wifi.Current
	wifiLoad       = wifi.Load
	wifiUse        = wifi.Use
	wifiSave       = wifi.Save
	wifiApply      = wifi.Apply
	wifiAddress    = func(iface string) string { return netconf.Current(iface).Address }
)

const (
	// joinTimeout is how long joining may take before the dialog gives up:
	// a scan, association, the handshake, and an enterprise network's round
	// trips to its RADIUS server.
	joinTimeout = 30 * time.Second
	// wifiRescan is how often the open dialog scans again. wpa_supplicant
	// scans by itself while it is on no network, and not at all once it is.
	wifiRescan = 15 * time.Second
	// wifiScanShown is how long after asking for a scan the dialog says it
	// is looking, which is about how long a scan of both bands takes.
	wifiScanShown = 5 * time.Second
)

// wifiItem is what the prompt's focus can be on.
type wifiItem int

const (
	itemName     wifiItem = iota // a hidden network's name
	itemSecurity                 // and how it lets clients in
	itemUser                     // an enterprise network's user name
	itemPassword
	itemJoin
	itemCancel
)

// hiddenSecurity is what a hidden network's security can be set to: the kinds
// joining one tells apart. WPA3-only (SAE) networks are joined from the list,
// where they say what they are.
var hiddenSecurity = []wifi.Security{wifi.Open, wifi.PSK, wifi.EAP}

// items lists what the prompt shows, in focus order: a hidden network's name
// and security, the user name of an enterprise network, the password of any
// but an open one, then the buttons.
func (w *WifiState) items() []wifiItem {
	var it []wifiItem
	if w.Hidden {
		it = append(it, itemName, itemSecurity)
	}
	if w.Net.Security == wifi.EAP {
		it = append(it, itemUser)
	}
	if w.Net.Security != wifi.Open {
		it = append(it, itemPassword)
	}
	return append(it, itemJoin, itemCancel)
}

// step moves the prompt's focus by d.
func (w *WifiState) step(d int) {
	it := w.items()
	i := 0
	for j, x := range it {
		if x == w.Focus {
			i = j
		}
	}
	w.Focus = it[(i+d+len(it))%len(it)]
}

// field is the text field the focus is on, or -1.
func (w *WifiState) field() int {
	switch w.Focus {
	case itemName, itemUser, itemPassword:
		return int(w.Focus)
	}
	return -1
}

// cycleSecurity steps a hidden network's security by d.
func (w *WifiState) cycleSecurity(d int) {
	i := 0
	for j, s := range hiddenSecurity {
		if s == w.Net.Security {
			i = j
		}
	}
	w.Net.Security = hiddenSecurity[(i+d+len(hiddenSecurity))%len(hiddenSecurity)]
}

// on reports whether the interface is on the network named ssid.
func (w *WifiState) on(ssid string) bool {
	return ssid != "" && w.Status.Joined() && w.Status.SSID == ssid
}

// move steps the list's selection by d, keeping it in view.
func (w *WifiState) move(d int) {
	w.Sel = clampInt(w.Sel+d, 0, len(w.Networks))
	w.scroll()
}

// scroll brings the selection into view.
func (w *WifiState) scroll() {
	if w.Sel < w.Top {
		w.Top = w.Sel
	}
	if w.Sel >= w.Top+wifiRows {
		w.Top = w.Sel - wifiRows + 1
	}
	w.Top = clampInt(w.Top, 0, max(0, len(w.Networks)+1-wifiRows))
}

// setNetworks takes in what a scan found, the selection staying on the
// network it was on as the order changes with the signal.
func (w *WifiState) setNetworks(nets []wifi.Network) {
	cur, hidden := "", w.Sel >= len(w.Networks) && len(w.Networks) > 0
	if w.Sel < len(w.Networks) {
		cur = w.Networks[w.Sel].SSID
	}
	w.Networks, w.Sel = nets, 0
	for i, n := range nets {
		if n.SSID == cur {
			w.Sel = i
		}
	}
	if hidden {
		w.Sel = len(nets)
	}
	w.scroll()
}

// initWifi points the dialog's state at the kiosk's first wireless interface,
// if it has one, and returns it. The kept network is read the first time
// only: the file it comes from holds a network being joined while it is.
// Callers must hold state.Mu.
func initWifi(state *UIState) string {
	ifs := wifiInterfaces()
	if len(ifs) == 0 {
		return ""
	}
	if w := &state.Wifi; w.Iface != ifs[0] {
		kept, _ := wifiLoad()
		*w = WifiState{Iface: ifs[0], Kept: kept}
	}
	return ifs[0]
}

// openWifi shows the wireless dialog. Callers must hold state.Mu.
func openWifi(state *UIState) {
	w := &state.Wifi
	w.Prompt, w.Msg, w.Err = false, "", false
	state.Transition(ScreenWifi)
	refreshWifi(state)
	scanWifi(state)
}

// closeWifi goes back to the network settings the dialog was opened from.
// Under DHCP, with no interface set, the dialog shows the interface the
// kiosk's network is on now — the wireless one, when one was joined here.
// Callers must hold state.Mu.
func closeWifi(state *UIState) {
	n := &state.Net
	if !n.Static && netconf.Load().Interface == "" {
		if i := pickInterface(); i != n.Iface {
			n.Iface = i
			ip, mask, gw, dns1, dns2 := netconf.Lease(i).Form()
			n.Fields = [netFields]string{ip, mask, gw, dns1, dns2}
		}
	}
	n.Status = netconf.Current(n.Iface)
	state.Transition(ScreenNetwork)
}

// scanWifi asks wpa_supplicant to look for networks; the polls pick up what
// it finds. Callers must hold state.Mu.
func scanWifi(state *UIState) {
	w := &state.Wifi
	if w.Iface == "" {
		return
	}
	w.ScanAt = time.Now()
	iface, scan := w.Iface, wifiScan
	go func() {
		if err := scan(iface); err != nil {
			log.Printf("wifi: scan: %v", err)
		}
	}()
}

// refreshWifi reads the networks in range and the interface's status in the
// background: wpa_cli is a process to run, which the render loop does not
// wait for. A network being joined is followed from here. Callers must hold
// state.Mu.
func refreshWifi(state *UIState) {
	w := &state.Wifi
	if w.polling || w.Iface == "" {
		return
	}
	w.polling = true
	iface, gen := w.Iface, w.gen
	results, current, address := wifiResults, wifiCurrent, wifiAddress
	go func() {
		nets, errN := results(iface)
		st, errS := current(iface)
		addr := address(iface)

		state.Mu.Lock()
		defer state.Mu.Unlock()
		w := &state.Wifi
		w.polling = false
		if w.Iface != iface || w.gen != gen {
			return // read before a change it would undo; the next poll is not
		}
		w.Down = errN != nil && errS != nil
		if errN == nil {
			w.setNetworks(nets)
		}
		if errS == nil {
			w.Status = st
		}
		w.Address = addr
		w.watch(state, time.Now())
		state.Dirty = true
	}()
}

// watch follows a network being joined: kept once the kiosk is on it; given
// up on, and the network before it put back, when the time is up or
// wpa_supplicant has refused it — which, after a handshake that failed, is a
// wrong password: the prompt opens again for another try. Callers must hold
// state.Mu.
func (w *WifiState) watch(state *UIState, now time.Time) {
	j := w.Joining
	switch {
	case j.SSID == "" || w.Busy:
		return
	case w.on(j.SSID):
		w.Joining = wifi.Config{}
		w.keep(state, j)
		return
	case !w.Status.Rejected && now.Before(w.JoinBy):
		return
	}
	w.Joining = wifi.Config{}
	if w.Status.Rejected {
		w.ask(wifi.Network{SSID: j.SSID, Security: j.Security}, false)
		w.Fields[itemUser] = j.Identity
		w.Focus = itemPassword
		w.Msg = i18n.T(i18n.WifiRejected)
		if j.Security == wifi.EAP {
			w.Msg = i18n.T(i18n.WifiRejectedUser)
		}
	} else {
		w.Msg = i18n.Tf(i18n.WifiFailed, j.SSID)
	}
	w.Err = true
	prev, use, apply := w.Prev, wifiUse, wifiApply
	w.gen++
	go func() {
		err := use(prev)
		if err == nil {
			err = apply()
		}
		if err != nil {
			log.Printf("wifi: going back to %q: %v", prev.SSID, err)
		}
	}()
}

// keep makes c the network the kiosk keeps, now that it is on it. Callers
// must hold state.Mu.
func (w *WifiState) keep(state *UIState, c wifi.Config) {
	w.Kept = c
	installed, save := state.Boot.Installed(), wifiSave
	go func() {
		persisted, err := save(c, installed)

		state.Mu.Lock()
		defer state.Mu.Unlock()
		w := &state.Wifi
		switch {
		case err != nil:
			log.Printf("wifi: keeping %q: %v", c.SSID, err)
			w.Msg, w.Err = i18n.Tf(i18n.NetErrApply, err.Error()), true
		case persisted:
			w.Msg, w.Err = i18n.T(i18n.NetSaved), false
		default:
			w.Msg, w.Err = i18n.T(i18n.NetSessionOnly), false
		}
		// A new network is a new subnet to scan; the network ticker in Run
		// starts that scan once it sees no subnet, and an address.
		state.Subnet = ""
		state.ScanDone = false
		state.Dirty = true
	}()
}

// join puts c in effect and has wpa_supplicant join it; watch follows it from
// there. Callers must hold state.Mu.
func (w *WifiState) join(state *UIState, c wifi.Config) {
	prev, use, apply := w.Kept, wifiUse, wifiApply
	w.Busy, w.Prompt = true, false
	w.Msg, w.Err = "", false // the status line follows it from here
	w.Joining = wifi.Config{}
	w.gen++
	go func() {
		err := use(c)
		if err == nil {
			if err = apply(); err != nil {
				use(prev)
			}
		}

		state.Mu.Lock()
		defer state.Mu.Unlock()
		w := &state.Wifi
		w.Busy = false
		w.gen++
		state.Dirty = true
		if err != nil {
			log.Printf("wifi: joining %q: %v", c.SSID, err)
			w.Msg, w.Err = i18n.Tf(i18n.NetErrApply, err.Error()), true
			return
		}
		// What wpa_supplicant reported before reading the new network says
		// nothing about it.
		w.Joining, w.Prev, w.JoinBy = c, prev, time.Now().Add(joinTimeout)
		w.Status = wifi.Status{}
	}()
}

// forget drops the kept network: the kiosk leaves it now, and does not look
// for it at the next boot. Callers must hold state.Mu.
func (w *WifiState) forget(state *UIState) {
	ssid := w.Kept.SSID
	if ssid == "" {
		return
	}
	installed, save, apply := state.Boot.Installed(), wifiSave, wifiApply
	w.Busy, w.Joining = true, wifi.Config{}
	w.gen++
	go func() {
		_, err := save(wifi.Config{}, installed)
		if err == nil {
			err = apply()
		}

		state.Mu.Lock()
		defer state.Mu.Unlock()
		w := &state.Wifi
		w.Busy = false
		w.gen++
		state.Dirty = true
		if err != nil {
			log.Printf("wifi: forgetting %q: %v", ssid, err)
			w.Msg, w.Err = i18n.Tf(i18n.NetErrApply, err.Error()), true
			return
		}
		w.Kept, w.Status = wifi.Config{}, wifi.Status{}
		w.Msg, w.Err = i18n.Tf(i18n.WifiForgotten, ssid), false
	}()
}

// choose acts on the selected row: joins an open network, or the kept one,
// right away, and asks for what any other takes. Callers must hold state.Mu.
func (w *WifiState) choose(state *UIState) {
	if w.Sel >= len(w.Networks) {
		w.ask(wifi.Network{Security: wifi.PSK}, true)
		return
	}
	n := w.Networks[w.Sel]
	switch {
	case w.on(n.SSID):
		// On it already.
	case n.SSID == w.Kept.SSID:
		w.join(state, w.Kept)
	case n.Security == wifi.WEP:
		w.Msg, w.Err = i18n.T(i18n.WifiErrWEP), true
	case n.Security == wifi.Open:
		c, _ := wifi.NewConfig(n, "", "")
		w.join(state, c)
	default:
		w.ask(n, false)
	}
}

// ask opens the prompt for n, or for a hidden network whose name is typed.
func (w *WifiState) ask(n wifi.Network, hidden bool) {
	w.Prompt, w.Net, w.Hidden = true, n, hidden
	w.Fields = [itemPassword + 1]string{}
	w.Msg, w.Err = "", false
	w.Focus = w.items()[0]
}

// submit joins the network the prompt is for with what was typed, or says
// what is missing. Callers must hold state.Mu.
func (w *WifiState) submit(state *UIState) {
	n := w.Net
	if w.Hidden {
		n.SSID = w.Fields[itemName]
	}
	c, err := wifi.NewConfig(n, w.Fields[itemUser], w.Fields[itemPassword])
	switch {
	case errors.Is(err, wifi.ErrName):
		w.Msg, w.Err, w.Focus = i18n.T(i18n.WifiErrName), true, itemName
	case errors.Is(err, wifi.ErrIdentity):
		w.Msg, w.Err, w.Focus = i18n.T(i18n.WifiErrUser), true, itemUser
	case errors.Is(err, wifi.ErrPassphrase) && w.Fields[itemPassword] == "":
		w.Msg, w.Err, w.Focus = i18n.T(i18n.WifiErrNoPassword), true, itemPassword
	case errors.Is(err, wifi.ErrPassphrase):
		w.Msg, w.Err, w.Focus = i18n.T(i18n.WifiErrPassword), true, itemPassword
	case err != nil:
		w.Msg, w.Err = i18n.T(i18n.WifiErrWEP), true
	default:
		w.join(state, c)
	}
}

// handleWifiKey drives the dialog. Callers must hold state.Mu.
func handleWifiKey(ev inputdev.InputEvent, state *UIState) {
	w := &state.Wifi
	if w.Busy {
		return
	}
	if w.Prompt {
		w.promptKey(ev, state)
		return
	}
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		closeWifi(state)
	case inputdev.KeyUp:
		w.move(-1)
	case inputdev.KeyDown:
		w.move(1)
	case inputdev.KeyPageUp:
		w.move(-wifiRows)
	case inputdev.KeyPageDown:
		w.move(wifiRows)
	case inputdev.KeyEnter:
		w.choose(state)
	case inputdev.KeyF5:
		scanWifi(state)
	case inputdev.KeyDelete:
		w.forget(state)
	}
}

// handleWifiClick is a click on the dialog at p: on a network, it is Enter on
// it. Callers must hold state.Mu.
func handleWifiClick(state *UIState, screen image.Rectangle, p image.Point) {
	w := &state.Wifi
	if w.Busy {
		return
	}
	l := layoutWifi(screen, w)
	if !w.Prompt {
		if i := l.rowAt(w, p.X, p.Y); i >= 0 {
			w.Sel = i
			w.choose(state)
		}
		return
	}
	for it, r := range l.Items {
		if p.In(r) && it != itemSecurity {
			w.Focus = it
		}
	}
	for i, r := range l.Chips {
		if p.In(r) {
			w.Net.Security, w.Focus = hiddenSecurity[i], itemSecurity
		}
	}
	switch {
	case p.In(l.Join):
		w.Focus = itemJoin
		w.submit(state)
	case p.In(l.Cancel):
		w.Prompt, w.Msg, w.Err = false, "", false
	}
}

// promptKey drives the prompt. Callers must hold state.Mu.
func (w *WifiState) promptKey(ev inputdev.InputEvent, state *UIState) {
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		w.Prompt, w.Msg, w.Err = false, "", false
	case inputdev.KeyTab, inputdev.KeyDown:
		w.step(1)
	case inputdev.KeyUp:
		w.step(-1)
	case inputdev.KeyLeft:
		if w.Focus == itemSecurity {
			w.cycleSecurity(-1)
		}
	case inputdev.KeyRight:
		if w.Focus == itemSecurity {
			w.cycleSecurity(1)
		}
	case inputdev.KeyEnter:
		switch w.Focus {
		case itemCancel:
			w.Prompt, w.Msg, w.Err = false, "", false
		case itemSecurity:
			w.step(1)
		default:
			w.submit(state)
		}
	case inputdev.KeyBackspace:
		if f := w.field(); f >= 0 {
			if r := []rune(w.Fields[f]); len(r) > 0 {
				w.Fields[f] = string(r[:len(r)-1])
			}
		}
	default:
		switch f := w.field(); {
		case w.Focus == itemSecurity && ev.KeyCode == inputdev.KeySpace:
			w.cycleSecurity(1)
		case f >= 0 && ev.Rune != 0:
			w.Fields[f] += string(ev.Rune)
		}
	}
}
