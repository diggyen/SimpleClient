package ui

import (
	"image"
	"net"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/autoconnect"
	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/rdp"
)

// recordSaves stands in for writing the startup setting, and returns what
// was written.
func recordSaves(t *testing.T) *[]autoconnect.Config {
	t.Helper()
	var saved []autoconnect.Config
	old := saveAuto
	saveAuto = func(c autoconnect.Config, _ bool) (bool, error) {
		saved = append(saved, c)
		return true, nil
	}
	t.Cleanup(func() { saveAuto = old })
	return &saved
}

func withNetwork(t *testing.T, cidr string) {
	t.Helper()
	old := detectCIDR
	detectCIDR = func() (string, error) { return cidr, nil }
	t.Cleanup(func() { detectCIDR = old })
}

// A startup server without a kept password opens its dialog, the account
// filled in and the cursor in the password.
func TestStartAuto_Dialog(t *testing.T) {
	state := &UIState{Auto: &autoconnect.Config{Mode: autoconnect.Dialog, Server: "10.7.7.101", Username: "ayse"}}
	if !state.startAuto(time.Now()) {
		t.Fatal("startAuto did nothing")
	}
	m := state.Modal
	if state.Screen != ScreenModal || m.Fields[fieldServer] != "10.7.7.101" || m.Fields[fieldUser] != "ayse" ||
		m.Fields[fieldPassword] != "" || m.FocusIdx != fieldPassword || m.Auto != autoconnect.Dialog {
		t.Fatalf("screen %v, dialog %+v", state.Screen, m)
	}
	if h := state.SelectedHost(); h == nil || h.Address() != "10.7.7.101" {
		t.Fatalf("the server is not the listed, selected host: %v", h)
	}
	if (&UIState{}).startAuto(time.Now()) {
		t.Fatal("startAuto acted without a startup server")
	}
}

// With the password kept, the kiosk counts down and logs on once it has an
// address; without one it waits, and says so.
func TestStartAuto_LogonCountsDown(t *testing.T) {
	now := time.Now()
	state := &UIState{Auto: &autoconnect.Config{Mode: autoconnect.Logon, Server: "10.7.7.101", Username: "kiosk", Password: "x"}}
	state.startAuto(now)
	if state.Screen != ScreenAuto || !state.AutoAt.Equal(now.Add(autoDelay)) {
		t.Fatalf("screen %v, logs on at %v", state.Screen, state.AutoAt.Sub(now))
	}
	if m := state.Modal; m.Fields[fieldPassword] != "x" || m.Auto != autoconnect.Logon {
		t.Fatalf("dialog %+v", m)
	}

	withNetwork(t, "")
	if state.autoDue(now.Add(time.Second)) {
		t.Fatal("due before the countdown is over")
	}
	if state.autoDue(now.Add(autoDelay)) || state.AutoNet {
		t.Fatal("due without an address")
	}
	if !state.AutoAt.Equal(now.Add(autoDelay + time.Second)) {
		t.Fatalf("looks for an address again after %v", state.AutoAt.Sub(now.Add(autoDelay)))
	}
	withNetwork(t, "10.7.7.0/24")
	if !state.autoDue(now.Add(autoDelay+time.Second)) || !state.AutoNet {
		t.Fatal("not due with an address")
	}
}

// Esc on the countdown goes to the list; Enter logs on at once.
func TestAutoKeys(t *testing.T) {
	withNetwork(t, "10.7.7.0/24")
	key := func(state *UIState, code int) {
		handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Pressed: true},
			state, nil, config.Config{}, framebuffer.NewMock(1280, 800), nil, nil, nil, nil, 20)
	}
	auto := &autoconnect.Config{Mode: autoconnect.Logon, Server: "10.7.7.101", Username: "kiosk"}

	state := &UIState{Auto: auto}
	state.startAuto(time.Now())
	key(state, inputdev.KeyEnter)
	if !state.autoDue(time.Now()) {
		t.Fatal("Enter did not cut the countdown short")
	}
	key(state, inputdev.KeyEsc)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("Esc left the screen at %v", state.Screen)
	}
}

// An unattended attempt that failed counts down again, longer, and says why.
func TestAutoFailed(t *testing.T) {
	now := time.Now()
	state := &UIState{Screen: ScreenConnecting}
	state.autoFailed(i18n.T(i18n.ErrTimeout), now)
	if state.Screen != ScreenAuto || state.AutoErr != i18n.T(i18n.ErrTimeout) || !state.AutoAt.Equal(now.Add(autoRetry)) {
		t.Fatalf("screen %v, error %q, again in %v", state.Screen, state.AutoErr, state.AutoAt.Sub(now))
	}
	if authFailed(errTest("connection refused")) || !authFailed(errTest("authentication failed")) {
		t.Fatal("authFailed sorts the errors wrongly")
	}
}

// F10 moves through the choices; turning it off for the startup server
// takes effect at once, turning it on waits for a connection that works.
func TestToggleAuto(t *testing.T) {
	saved := recordSaves(t)
	state := &UIState{
		Screen: ScreenModal,
		Hosts:  []domain.Host{{IP: net.ParseIP("10.7.7.101")}},
		Auto:   &autoconnect.Config{Mode: autoconnect.Logon, Server: "10.7.7.101", Username: "kiosk", Password: "x"},
	}
	state.OpenModalFor()
	if state.Modal.Auto != autoconnect.Logon || state.Modal.FocusIdx != state.Modal.focusConnect() {
		t.Fatalf("the startup server's dialog: %+v", state.Modal)
	}
	f10 := func() {
		handleModalKey(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: inputdev.KeyF10, Pressed: true},
			state, framebuffer.NewMock(1280, 800), nil)
	}
	f10()
	if state.Modal.Auto != autoconnect.Off || state.Auto != nil || len(*saved) != 1 || (*saved)[0].Mode != autoconnect.Off {
		t.Fatalf("off: dialog %v, setting %v, saved %+v", state.Modal.Auto, state.Auto, *saved)
	}
	f10()
	f10()
	if state.Modal.Auto != autoconnect.Logon || len(*saved) != 1 {
		t.Fatalf("on again: dialog %v, saved %+v", state.Modal.Auto, *saved)
	}

	// The toggle is where it is drawn.
	l := layoutModal(image.Rect(0, 0, 1280, 800), state.Modal.fieldCount())
	handleMouseClick(l.Auto.Min.X+5, l.Auto.Min.Y+5, state, framebuffer.NewMock(1280, 800), nil)
	if state.Modal.Auto != autoconnect.Off {
		t.Fatalf("a click on the toggle left it at %v", state.Modal.Auto)
	}
}

// What a working connection keeps: the account, the password only to log on
// with, nothing for a dialog that was left off.
func TestKeepAuto(t *testing.T) {
	state := &UIState{}
	creds := rdp.Credentials{Username: "kiosk", Password: "x", Domain: "OFIS"}
	if _, changed := state.keepAuto("10.7.7.101", creds, autoconnect.Off); changed || state.Auto != nil {
		t.Fatal("off kept something")
	}
	c, changed := state.keepAuto(" 10.7.7.101 ", creds, autoconnect.Dialog)
	if !changed || c != (autoconnect.Config{Mode: autoconnect.Dialog, Server: "10.7.7.101", Username: "kiosk", Domain: "OFIS"}) {
		t.Fatalf("dialog kept %+v (%v)", c, changed)
	}
	if c, changed = state.keepAuto("10.7.7.101", creds, autoconnect.Logon); !changed || c.Password != "x" {
		t.Fatalf("logon kept %+v (%v)", c, changed)
	}
	if _, changed = state.keepAuto("10.7.7.101", creds, autoconnect.Logon); changed {
		t.Fatal("the same setting written again")
	}
	// An F7 dialog for the startup server starts off, and leaves it alone.
	if _, changed = state.keepAuto("10.7.7.101", creds, autoconnect.Off); changed || state.Auto == nil {
		t.Fatal("a dialog that started off dropped the startup server")
	}
	if c, _ = state.keepAuto("10.7.7.101", rdp.Credentials{}, autoconnect.Logon); c.Mode != autoconnect.Dialog {
		t.Fatalf("logging on without an account kept %+v", c)
	}
}

// The startup server is recognised however the dialog came to hold it.
func TestIsAutoServer(t *testing.T) {
	byIP := &UIState{Auto: &autoconnect.Config{Mode: autoconnect.Dialog, Server: "10.7.7.101"}}
	byName := &UIState{Auto: &autoconnect.Config{Mode: autoconnect.Dialog, Server: "rdp.example.com:53389"}}
	listed := domain.Host{IP: net.ParseIP("10.7.7.101")}
	resolved := domain.Host{IP: net.ParseIP("192.0.2.7"), Hostname: "rdp.example.com", Port: 53389}
	for _, c := range []struct {
		state *UIState
		typed string
		host  domain.Host
		want  bool
	}{
		{byIP, "10.7.7.101", domain.Host{}, true},
		{byIP, "10.7.7.101:3389", domain.Host{}, true},
		{byIP, "", listed, true},
		{byIP, "10.7.7.102", domain.Host{}, false},
		{byName, "RDP.example.com:53389", domain.Host{}, true},
		{byName, "192.0.2.7:53389", resolved, true},
		{byName, "192.0.2.7", domain.Host{IP: net.ParseIP("192.0.2.7")}, false},
		{&UIState{}, "10.7.7.101", listed, false},
	} {
		if got := c.state.isAutoServer(c.typed, c.host); got != c.want {
			t.Errorf("%q / %v against %q: %v", c.typed, c.host, c.state.Auto, got)
		}
	}
}

// A session that ends goes back to where the kiosk starts, and one that
// drops shows the connecting screen while it is reconnected.
func TestTransition_Auto(t *testing.T) {
	for _, c := range [][2]Screen{
		{ScreenDiscovery, ScreenAuto},
		{ScreenAuto, ScreenConnecting},
		{ScreenConnecting, ScreenAuto},
		{ScreenSession, ScreenAuto},
		{ScreenSession, ScreenModal},
		{ScreenSession, ScreenConnecting},
	} {
		state := &UIState{Screen: c[0]}
		state.Transition(c[1])
		if state.Screen != c[1] {
			t.Errorf("%v -> %v blocked", c[0], c[1])
		}
	}
}

// The toggle's longest label fits the dialog in both languages, and the
// countdown draws.
func TestRenderAuto(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })
	screen := image.Rect(0, 0, 640, 480)
	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		l := layoutModal(screen, 4)
		for _, m := range []autoconnect.Mode{autoconnect.Off, autoconnect.Dialog, autoconnect.Logon} {
			if w := TextWidth("F10", false) + 10 + 7 + TextWidth(autoLabel(m), false); l.Auto.Min.X+w > l.Box.Max.X-modalPad/2 {
				t.Errorf("%s: %q runs out of the dialog", lang, autoLabel(m))
			}
		}
		if l.Auto.Min.Y < l.Domain.Max.Y || l.Auto.Max.Y > l.Connect.Min.Y {
			t.Errorf("%s: the toggle %v sits on the domain toggle %v or the buttons %v", lang, l.Auto, l.Domain, l.Connect)
		}
	}

	back := image.NewRGBA(screen)
	state := &UIState{Screen: ScreenAuto, AutoAt: time.Now().Add(3 * time.Second), AutoNet: true}
	state.Modal.Fields[fieldServer] = "10.7.7.101"
	renderAuto(back, state)
	if countInk(back) == 0 {
		t.Fatal("the countdown drew nothing")
	}
}
