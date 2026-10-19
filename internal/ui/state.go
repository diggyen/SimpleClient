package ui

import (
	"sync"
	"time"

	"github.com/diggyen/SimpleClient/internal/autoconnect"
	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/domain"
)

// Screen identifies which UI screen is currently active.
type Screen int

const (
	ScreenDiscovery  Screen = iota // Host list / scanning
	ScreenModal                    // Credential entry modal
	ScreenConnecting               // "Connecting..." overlay
	ScreenSession                  // Active RDP session
	ScreenInstall                  // Install-to-disk dialog
	ScreenNetwork                  // Network settings dialog
	ScreenAuto                     // Counting down to logging on to the startup server
	ScreenWifi                     // Wireless networks, from the network settings
)

// ModalState holds the state of the connection dialog.
//
// The server field comes first and is editable. Picked from the list it holds
// that host's address; opened with F7 it is empty, for a server the scan
// cannot see — the kiosk scans its own subnet only, and the desktops it serves
// may well sit on another one, a VLAN of virtual machines for instance.
//
// The domain field is an option and starts hidden. The desktops this kiosk
// mostly reaches are Linux, where a domain means nothing and an empty box
// labelled "Domain" only raises the question of what to put in it. F6, or a
// click on its hint, shows it for a Windows domain account, and it comes up
// shown for a host whose remembered account has one. DOMAIN\user typed into
// the username field works either way.
type ModalState struct {
	Fields     [4]string // indexed by fieldServer ... fieldDomain
	ShowDomain bool
	FocusIdx   int    // a field, then the connect button, then cancel
	Error      string // Error message to display
	// Auto is what the kiosk is to do with this server at startup (F10),
	// kept once a connection with it works; see autoconnect.go.
	Auto autoconnect.Mode
}

// The dialog's text fields, in focus order.
const (
	fieldServer = iota
	fieldUser
	fieldPassword
	fieldDomain
)

// fieldCount is how many text fields the dialog shows.
func (m *ModalState) fieldCount() int {
	if m.ShowDomain {
		return 4
	}
	return 3
}

// focusConnect and focusCancel are the buttons' places in the focus order,
// right after the fields.
func (m *ModalState) focusConnect() int { return m.fieldCount() }
func (m *ModalState) focusCancel() int  { return m.fieldCount() + 1 }

// domain is the domain to log on to: none while the field is hidden, even if
// something was typed into it before.
func (m *ModalState) domain() string {
	if m.ShowDomain {
		return m.Fields[fieldDomain]
	}
	return ""
}

// toggleDomain shows or hides the domain field. Shown, it takes the focus,
// since typing the domain is why it was asked for; hidden, a focused button
// stays focused and focus on the field itself falls back to the password.
func (m *ModalState) toggleDomain() {
	button := m.FocusIdx - m.fieldCount() // >= 0 while a button has focus
	m.ShowDomain = !m.ShowDomain
	switch {
	case m.ShowDomain:
		m.FocusIdx = fieldDomain
	case button >= 0:
		m.FocusIdx = m.fieldCount() + button
	case m.FocusIdx == fieldDomain:
		m.FocusIdx = fieldPassword
	}
}

// UIState is the complete mutable state of the UI layer.
type UIState struct {
	Mu sync.Mutex

	// Dirty marks the state as needing a repaint. The render loop only draws
	// when it is set, so anything that changes what is on screen has to set it
	// — including the connect goroutine, whose result would otherwise never be
	// painted and would leave the kiosk showing "Connecting..." forever.
	Dirty bool

	Screen Screen
	// Subnet is the network the last scan covered, shown when it found
	// nothing: "no servers in ?" is what a machine whose NIC never came up
	// looks like, and saying so saves chasing DHCP on a dead interface.
	Subnet       string
	Hosts        []domain.Host
	SelectedIdx  int
	ScrollOffset int
	ScanProgress float64 // [0, 1]
	ScanDone     bool
	Modal        ModalState
	ErrorMsg     string // Transient info/error banner

	// Boot is how this kiosk was started. Installing to disk is offered
	// (CanInstall) only when it runs from a USB stick or CD: a kiosk booted
	// from its installed disk has nothing to install, and F9 there would only
	// wipe the disk it runs from.
	Boot       bootenv.Boot
	CanInstall bool
	Install    InstallState

	Net  NetState
	Wifi WifiState

	MouseX int
	MouseY int

	// lastUser remembers, per host address, the username and domain that last
	// connected successfully. Reconnecting to the same machine is the normal
	// case — a dropped session, a reboot at the far end — and retyping the
	// account every time is the bulk of the work at a kiosk with no keyboard
	// of its own worth speaking of. The password is deliberately never part of
	// it.
	//
	// An installed kiosk also keeps it, with the servers it has connected to,
	// in SavePath on its data partition (see saved.go); one running from a USB
	// stick or CD has nowhere to keep anything and starts empty every time.
	lastUser    map[string]credentialHint
	SavePath    string
	SpinnerTick int // Used for connecting-screen spinner animation

	// Auto is the server to go to at startup and after each session there,
	// nil without one. While the auto screen counts down, AutoAt is when it
	// logs on, AutoNet whether the kiosk has an address to do it from, and
	// AutoErr why the last attempt failed. unattended marks the connection
	// the countdown started, for connect to know a failure is to be tried
	// again rather than shown in the dialog.
	Auto       *autoconnect.Config
	AutoAt     time.Time
	AutoNet    bool
	AutoErr    string
	unattended bool

	// pointer is the input reader, set once by Run before anything else
	// runs; nil in tests.
	pointer pointer
}

// AddHost puts h in the list unless the same endpoint is already there, and
// returns its index. A known name is kept when the other record has none.
func (s *UIState) AddHost(h domain.Host) int {
	for i, o := range s.Hosts {
		if o.Same(h) {
			if s.Hosts[i].Hostname == "" {
				s.Hosts[i].Hostname = h.Hostname
			}
			return i
		}
	}
	s.Hosts = append(s.Hosts, h)
	s.Dirty = true
	return len(s.Hosts) - 1
}

// AddStaticHost puts a hand-configured address into the list ahead of any
// scanning: rdphost=<ip>[:port] on the kernel command line, the boot-time
// counterpart of typing an address with F7. If the address is also
// discoverable, the scan simply fills in its latency.
func (s *UIState) AddStaticHost(raw string) bool {
	h, err := domain.ParseIPAddress(raw)
	if err != nil {
		return false
	}
	s.AddHost(h)
	return true
}

// credentialHint is the non-secret half of a credential.
type credentialHint struct {
	Username string
	Domain   string
}

// RememberCredential records the account that just connected to addr.
// The password is not stored, and callers must not pass it.
func (s *UIState) RememberCredential(addr, username, domain string) {
	if addr == "" || username == "" {
		return
	}
	if s.lastUser == nil {
		s.lastUser = make(map[string]credentialHint)
	}
	s.lastUser[addr] = credentialHint{Username: username, Domain: domain}
}

// OpenModalFor prepares the connection dialog for the selected host, filling in
// its address and the account that last worked on it, and putting the cursor
// where there is still something to type.
func (s *UIState) OpenModalFor() {
	s.Modal = ModalState{}

	host := s.SelectedHost()
	if host == nil {
		return
	}
	s.Modal.Fields[fieldServer] = host.Address()
	s.Modal.FocusIdx = fieldUser
	if s.isAutoServer(host.Address(), *host) {
		s.fillFromAuto()
		return
	}
	hint, ok := s.lastUser[host.AddrRDP()]
	if !ok {
		return
	}

	s.Modal.Fields[fieldUser] = hint.Username
	s.Modal.Fields[fieldDomain] = hint.Domain
	s.Modal.ShowDomain = hint.Domain != ""
	// Straight to the password: it is the only field left to fill, and the only
	// one that cannot be remembered.
	s.Modal.FocusIdx = fieldPassword
}

// OpenModalManual prepares an empty connection dialog for a server typed in by
// hand, cursor in the server field.
func (s *UIState) OpenModalManual() {
	s.Modal = ModalState{FocusIdx: fieldServer}
}

// Transition moves the UI to a new screen.
// Only whitelisted transitions are allowed per IMPLEMENTATION.md 2.1.
func (s *UIState) Transition(to Screen) {
	s.Dirty = true

	// A session that ends goes back to where the kiosk starts: the list,
	// or the startup server's dialog or countdown. One that drops is
	// reconnected, with the connecting screen up meanwhile.
	allowed := map[Screen][]Screen{
		ScreenDiscovery:  {ScreenModal, ScreenInstall, ScreenNetwork, ScreenAuto},
		ScreenModal:      {ScreenDiscovery, ScreenConnecting},
		ScreenConnecting: {ScreenSession, ScreenModal, ScreenAuto},
		ScreenSession:    {ScreenDiscovery, ScreenConnecting, ScreenModal, ScreenAuto},
		ScreenAuto:       {ScreenConnecting, ScreenModal},
		ScreenNetwork:    {ScreenWifi},
		ScreenWifi:       {ScreenNetwork},
	}
	for _, valid := range allowed[s.Screen] {
		if valid == to {
			s.Screen = to
			return
		}
	}
	// Allow same-screen transitions (no-op) and any transition to Discovery (fallback).
	if to == s.Screen || to == ScreenDiscovery {
		s.Screen = to
	}
}

// SelectedHost returns a pointer to the currently selected host, or nil.
func (s *UIState) SelectedHost() *domain.Host {
	if len(s.Hosts) == 0 {
		return nil
	}
	idx := s.SelectedIdx
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s.Hosts) {
		idx = len(s.Hosts) - 1
	}
	return &s.Hosts[idx]
}

// VisibleHosts returns the slice of hosts that should be rendered given the
// current scroll offset and maximum displayable rows.
func (s *UIState) VisibleHosts(maxRows int) []domain.Host {
	if len(s.Hosts) == 0 {
		return nil
	}
	start := s.ScrollOffset
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > len(s.Hosts) {
		end = len(s.Hosts)
	}
	return s.Hosts[start:end]
}

// HandleScanEvent updates state in response to a scanner event.
func (s *UIState) HandleScanEvent(ev domain.ScanEvent) {
	s.Dirty = true

	switch ev.Type {
	case domain.EventHostFound:
		if ev.Host != nil {
			// Append and deduplicate by endpoint. A host already listed — typed
			// in, remembered from an earlier boot — takes the scan's latency and
			// keeps the name it was given if the scan found none.
			existing := false
			for i, h := range s.Hosts {
				if h.Same(*ev.Host) {
					name := h.Hostname
					s.Hosts[i] = *ev.Host
					if s.Hosts[i].Hostname == "" {
						s.Hosts[i].Hostname = name
					}
					existing = true
					break
				}
			}
			if !existing {
				s.Hosts = append(s.Hosts, *ev.Host)
			}
		}

	case domain.EventScanProgress:
		if ev.Total > 0 {
			s.ScanProgress = float64(ev.Scanned) / float64(ev.Total)
		}

	case domain.EventScanComplete:
		s.ScanProgress = 1.0
		s.ScanDone = true
	}
}

// MoveSelection moves the host list cursor by delta, clamped to valid range.
func (s *UIState) MoveSelection(delta, maxRows int) {
	s.Dirty = true

	s.SelectedIdx += delta
	if s.SelectedIdx < 0 {
		s.SelectedIdx = 0
	}
	if s.SelectedIdx >= len(s.Hosts) {
		s.SelectedIdx = len(s.Hosts) - 1
	}
	// Adjust scroll so cursor stays visible.
	if s.SelectedIdx < s.ScrollOffset {
		s.ScrollOffset = s.SelectedIdx
	}
	if s.SelectedIdx >= s.ScrollOffset+maxRows {
		s.ScrollOffset = s.SelectedIdx - maxRows + 1
	}
}
