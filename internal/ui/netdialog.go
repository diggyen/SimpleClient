package ui

import (
	"errors"
	"log"
	"strconv"
	"strings"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/netconf"
)

// NetState is the network settings dialog (F8): DHCP, or a fixed address for
// a network without a DHCP server — without one the kiosk has no address, and
// nothing to scan or connect from. The address fields are there only while
// "manual" is chosen; with DHCP there is nothing to fill in. The port the
// server list scans for is there either way: RDP's 3389, or the one a network
// moved its servers to. On a kiosk with a wireless interface a row under them
// opens the wireless networks (wifi.go).
type NetState struct {
	Static bool
	Fields [netFields]string // indexed by netAddress ... netDNS2
	Focus  int               // netFocusMode, 1+field, port, wireless, then save and cancel
	Port   string            // the port field; 3389 unless set otherwise
	Iface  string
	Status netconf.Status
	Msg    string // the outcome of the last save
	Err    bool   // whether Msg is an error
	Busy   bool   // applying; keys are ignored meanwhile
	// Wireless is the wireless interface, if there is one.
	Wireless string
}

const (
	netAddress = iota
	netMask
	netGateway
	netDNS1 // preferred
	netDNS2 // alternate
	netFields
)

// The focus order: the DHCP/manual switch, the fields (manual only), the port,
// the wireless row (with a wireless interface only), the buttons.
const (
	netFocusMode   = 0
	netFocusPort   = 1 + netFields
	netFocusWifi   = 2 + netFields
	netFocusSave   = 3 + netFields
	netFocusCancel = 4 + netFields
	netFocusCount  = 5 + netFields
)

// field returns the field index the focus is on, or -1.
func (n *NetState) field() int {
	if n.Focus >= 1 && n.Focus <= netFields {
		return n.Focus - 1
	}
	return -1
}

// move steps the focus, past the fields while DHCP is chosen and they are not
// shown, and past the wireless row on a kiosk without it.
func (n *NetState) move(d int) {
	for {
		n.Focus = (n.Focus + d + netFocusCount) % netFocusCount
		switch {
		case n.field() >= 0 && !n.Static:
		case n.Focus == netFocusWifi && n.Wireless == "":
		default:
			return
		}
	}
}

// openNetwork shows the dialog with the settings in effect. Callers must hold
// state.Mu.
func openNetwork(state *UIState) {
	c := netconf.Load()
	port := state.ScanPort
	if port == 0 {
		port = domain.DefaultRDPPort
	}
	n := NetState{Static: c.Static, Iface: c.Interface, Port: strconv.Itoa(port)}
	if n.Iface == "" {
		n.Iface = pickInterface()
	}
	if !c.Static {
		// What DHCP handed out is the starting point for a fixed address: on
		// most networks it is one digit away from the right one.
		c = netconf.Lease(n.Iface)
	}
	ip, mask, gw, dns1, dns2 := c.Form()
	n.Fields = [netFields]string{ip, mask, gw, dns1, dns2}
	n.Status = netconf.Current(n.Iface)
	n.Wireless = initWifi(state)
	state.Net = n
	state.Transition(ScreenNetwork)
	refreshWifi(state)
}

// pickInterface is the interface a fixed address goes on when none is set: the
// one the kiosk has its network on — an address other than the link-local
// fallback — then the first with a cable in or a wireless network joined, or
// the first there is. A cable in a port that leads nowhere does not take it
// from the wireless network the kiosk is using.
func pickInterface() string {
	all := netconf.Interfaces()
	for _, i := range all {
		if a := netconf.Current(i).Address; a != "" && !strings.HasPrefix(a, "169.254.") {
			return i
		}
	}
	for _, i := range all {
		if netconf.Current(i).Carrier {
			return i
		}
	}
	if len(all) > 0 {
		return all[0]
	}
	return ""
}

// handleNetKey drives the dialog. Callers must hold state.Mu.
func handleNetKey(ev inputdev.InputEvent, state *UIState) {
	n := &state.Net
	if n.Busy {
		return
	}
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		state.Transition(ScreenDiscovery)
	case inputdev.KeyTab, inputdev.KeyDown:
		n.move(1)
	case inputdev.KeyUp:
		n.move(-1)
	case inputdev.KeyLeft, inputdev.KeyRight:
		if n.Focus == netFocusMode {
			n.Static = !n.Static
		}
	case inputdev.KeySpace:
		if n.Focus == netFocusMode {
			n.Static = !n.Static
		}
	case inputdev.KeyEnter:
		switch n.Focus {
		case netFocusCancel:
			state.Transition(ScreenDiscovery)
		case netFocusMode:
			n.Static = !n.Static
		case netFocusWifi:
			openWifi(state)
		default:
			saveNetwork(state)
		}
	case inputdev.KeyBackspace:
		if t := n.text(); t != nil {
			if r := []rune(*t); len(r) > 0 {
				*t = string(r[:len(r)-1])
			}
		}
	default:
		t := n.text()
		switch {
		case t == nil || ev.Rune == 0:
		case n.Focus == netFocusPort && (ev.Rune < '0' || ev.Rune > '9' || len(*t) >= 5):
		default:
			*t += string(ev.Rune)
		}
	}
}

// text returns the text the focus is on — a field, the port — or nil.
func (n *NetState) text() *string {
	if f := n.field(); f >= 0 {
		return &n.Fields[f]
	}
	if n.Focus == netFocusPort {
		return &n.Port
	}
	return nil
}

// saveNetwork checks the fields, then saves and applies the settings in the
// background: asking for a DHCP lease can take seconds. Callers must hold
// state.Mu.
func saveNetwork(state *UIState) {
	n := &state.Net
	c, err := netconf.FromForm(n.Static, n.Iface,
		n.Fields[netAddress], n.Fields[netMask], n.Fields[netGateway],
		n.Fields[netDNS1], n.Fields[netDNS2])
	if err != nil {
		n.Msg, n.Err = netFormError(err), true
		switch {
		case errors.Is(err, netconf.ErrMask):
			n.Focus = 1 + netMask
		case errors.Is(err, netconf.ErrGateway):
			n.Focus = 1 + netGateway
		case errors.Is(err, netconf.ErrDNS):
			n.Focus = 1 + netDNS1
		default:
			n.Focus = 1 + netAddress
		}
		return
	}
	if c.Port, err = netconf.ParsePort(n.Port); err != nil {
		n.Msg, n.Err, n.Focus = i18n.T(i18n.NetErrPort), true, netFocusPort
		return
	}
	n.Busy, n.Msg, n.Err = true, i18n.T(i18n.NetApplying), false
	installed := state.Boot.Installed()
	log.Printf("network: saving %q", c.Encode())

	go func() {
		persisted, err := netconf.Save(c, installed)
		saved := err == nil
		if err == nil {
			err = netconf.Apply()
		}
		st := netconf.Current(c.Interface)

		state.Mu.Lock()
		defer state.Mu.Unlock()
		n := &state.Net
		n.Busy, n.Status = false, st
		switch {
		case err != nil:
			log.Printf("network: %v", err)
			n.Msg, n.Err = i18n.Tf(i18n.NetErrApply, err.Error()), true
		case persisted:
			n.Msg, n.Err = i18n.T(i18n.NetSaved), false
		default:
			n.Msg, n.Err = i18n.T(i18n.NetSessionOnly), false
		}
		// A new address is a new subnet to scan; the network ticker in Run
		// starts that scan once it sees no subnet. A new port starts the list
		// afresh too, as F5 does: what is in it was found on the old one.
		if saved && c.Port != state.ScanPort {
			state.ScanPort = c.Port
			state.Hosts = nil
		}
		state.Subnet = ""
		state.ScanDone = false
		state.ScanProgress = 0
		state.Dirty = true
	}()
}

// netFormError is the message for a field FromForm rejected.
func netFormError(err error) string {
	switch {
	case errors.Is(err, netconf.ErrMask):
		return i18n.T(i18n.NetErrMask)
	case errors.Is(err, netconf.ErrGateway):
		return i18n.T(i18n.NetErrGateway)
	case errors.Is(err, netconf.ErrDNS):
		return i18n.T(i18n.NetErrDNS)
	}
	return i18n.T(i18n.NetErrAddress)
}
