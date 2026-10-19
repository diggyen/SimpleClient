package ui

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/diggyen/SimpleClient/internal/autoconnect"
	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/network"
	"github.com/diggyen/SimpleClient/internal/rdp"
)

// The startup server: F10 in the connection dialog moves between the server
// list, this server's dialog, and logging on to it with the password kept.
// The kiosk then starts there, and goes back there when a session with it
// ends — the dialog, or a countdown to logging on again that Esc leaves for
// the server list.

// autoDelay is the countdown before logging on, at startup and after a
// session: time to press Esc for the server list instead.
const autoDelay = 5 * time.Second

// autoRetry is the wait after an attempt that failed for a reason time may
// cure: a server still starting, a network not up yet.
const autoRetry = 10 * time.Second

// Hooks for tests.
var (
	saveAuto   = autoconnect.Save
	detectCIDR = network.DetectCIDR
)

// isAutoServer reports whether a dialog's server — as typed, and the listed
// host it is, if any — is the startup server.
func (s *UIState) isAutoServer(typed string, h domain.Host) bool {
	a := s.Auto
	if a == nil {
		return false
	}
	typed = strings.TrimSpace(typed)
	if strings.EqualFold(typed, a.Server) {
		return true
	}
	if ah, err := domain.ParseIPAddress(a.Server); err == nil {
		if th, err := domain.ParseIPAddress(typed); err == nil {
			return ah.Same(th)
		}
		return h.IP != nil && ah.Same(h)
	}
	// A name, which the list holds under the address it resolved to.
	name := a.Server
	if n, _, err := net.SplitHostPort(name); err == nil {
		name = n
	}
	return h.Hostname != "" && strings.EqualFold(h.Hostname, name)
}

// dialogHost is the listed host the dialog's server field holds, if it holds
// one.
func (s *UIState) dialogHost() domain.Host {
	if h := s.SelectedHost(); h != nil && h.Address() == strings.TrimSpace(s.Modal.Fields[fieldServer]) {
		return *h
	}
	return domain.Host{}
}

// fillFromAuto fills the dialog in with the startup server's account, and
// puts the cursor where there is still something to do.
func (s *UIState) fillFromAuto() {
	a, m := s.Auto, &s.Modal
	m.Auto = a.Mode
	m.Fields[fieldUser] = a.Username
	m.Fields[fieldDomain] = a.Domain
	m.Fields[fieldPassword] = a.Password // kept for Logon only
	m.ShowDomain = a.Domain != ""
	switch {
	case a.Username == "":
		m.FocusIdx = fieldUser
	case a.Mode == autoconnect.Logon:
		m.FocusIdx = m.focusConnect()
	default:
		m.FocusIdx = fieldPassword
	}
}

// openAutoModal prepares the dialog for the startup server. An address is
// put in the list, as rdphost= does; a name joins it once it resolves.
func (s *UIState) openAutoModal() {
	s.Modal = ModalState{}
	s.Modal.Fields[fieldServer] = s.Auto.Server
	if h, err := domain.ParseIPAddress(s.Auto.Server); err == nil {
		s.SelectedIdx = s.AddHost(h)
		s.Modal.Fields[fieldServer] = h.Address()
	}
	s.fillFromAuto()
}

// startAuto puts up what the kiosk shows for the startup server, at startup
// and when a session with it ends: its dialog, or the countdown to logging
// on. It reports false when there is no startup server.
func (s *UIState) startAuto(now time.Time) bool {
	if s.Auto == nil {
		return false
	}
	s.openAutoModal()
	if s.Auto.Mode != autoconnect.Logon {
		s.Transition(ScreenModal)
		return true
	}
	s.AutoAt, s.AutoNet, s.AutoErr = now.Add(autoDelay), true, ""
	s.Transition(ScreenAuto)
	return true
}

// autoDue reports whether the countdown is over and the kiosk has an address
// to log on from. Without one it looks again every second, and says so.
func (s *UIState) autoDue(now time.Time) bool {
	if now.Before(s.AutoAt) {
		return false
	}
	if cidr, _ := detectCIDR(); cidr == "" {
		s.AutoNet = false
		s.AutoAt = now.Add(time.Second)
		return false
	}
	s.AutoNet = true
	return true
}

// autoFailed goes back to the countdown after an unattended attempt that
// failed for a reason time may cure. One the server turned the account down
// for goes to the dialog instead, to be corrected there.
func (s *UIState) autoFailed(msg string, now time.Time) {
	s.AutoErr = msg
	s.AutoAt = now.Add(autoRetry)
	s.Transition(ScreenAuto)
}

// toggleAuto moves the dialog's startup choice on (F10). Turning it off for
// the server that has it takes effect at once; turning it on, once a
// connection made with it works.
func (s *UIState) toggleAuto() {
	m := &s.Modal
	m.Auto = m.Auto.Next()
	if m.Auto != autoconnect.Off || !s.isAutoServer(m.Fields[fieldServer], s.dialogHost()) {
		return
	}
	s.Auto = nil
	if _, err := saveAuto(autoconnect.Config{}, s.Boot.Installed()); err != nil {
		log.Printf("ui: startup server: %v", err)
	}
}

// keepAuto records the dialog's startup choice once a connection made with
// it has worked, and returns the setting to keep when that changed it. Off
// changes nothing here: turning the choice off has taken effect already, and
// a dialog that merely started off — F7, an address typed in — is no reason
// to drop the startup server.
func (s *UIState) keepAuto(server string, creds rdp.Credentials, mode autoconnect.Mode) (autoconnect.Config, bool) {
	if mode == autoconnect.Off {
		return autoconnect.Config{}, false
	}
	// Logging on takes an account; without one the server's own logon
	// screen is the most there is to start at.
	if mode == autoconnect.Logon && creds.Username == "" {
		mode = autoconnect.Dialog
	}
	c := autoconnect.Config{Mode: mode, Server: strings.TrimSpace(server),
		Username: creds.Username, Domain: creds.Domain}
	if mode == autoconnect.Logon {
		c.Password = creds.Password
	}
	if s.Auto != nil && *s.Auto == c {
		return c, false
	}
	s.Auto = &c
	return c, true
}

// handleAutoKey is the countdown's keys: Enter logs on at once, Esc goes to
// the server list.
func handleAutoKey(ev inputdev.InputEvent, state *UIState) {
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		state.Transition(ScreenDiscovery)
	case inputdev.KeyEnter:
		state.AutoAt = time.Time{}
	}
}
