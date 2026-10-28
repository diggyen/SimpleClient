package ui

import (
	"context"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/install"
	"github.com/diggyen/SimpleClient/internal/wifi"
)

// mockScanner implements domain.Scanner for loop tests.
type mockScanner struct {
	events []domain.ScanEvent
	port   int // the port the last scan was started for
}

func (m *mockScanner) Start(_ context.Context, _ []string, port int) <-chan domain.ScanEvent {
	m.port = port
	ch := make(chan domain.ScanEvent, len(m.events)+1)
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch
}
func (m *mockScanner) Cancel()              {}
func (m *mockScanner) Hosts() []domain.Host { return nil }

func TestHandleScanEvents_AddsHosts(t *testing.T) {
	state := &UIState{}
	for i := 0; i < 10; i++ {
		state.HandleScanEvent(domain.ScanEvent{
			Type: domain.EventHostFound,
			Host: &domain.Host{IP: net.ParseIP("10.0.0.1"), DiscoveredAt: time.Now()},
		})
	}
	if len(state.Hosts) != 1 {
		t.Fatalf("expected 1 host (dedup), got %d", len(state.Hosts))
	}
}

func TestHandleScanEvents_ManyHosts(t *testing.T) {
	state := &UIState{}
	for i := 0; i < 10; i++ {
		ip := net.ParseIP("10.0.0." + string(rune('1'+i)))
		state.HandleScanEvent(domain.ScanEvent{
			Type: domain.EventHostFound,
			Host: &domain.Host{IP: ip, DiscoveredAt: time.Now()},
		})
	}
	state.HandleScanEvent(domain.ScanEvent{Type: domain.EventScanComplete, Scanned: 10, Total: 10})
	if len(state.Hosts) != 10 {
		t.Fatalf("expected 10 hosts, got %d", len(state.Hosts))
	}
	if !state.ScanDone {
		t.Fatal("expected ScanDone = true after EventScanComplete")
	}
}

func TestTransition_EnterModal(t *testing.T) {
	state := &UIState{
		Screen: ScreenDiscovery,
		Hosts:  []domain.Host{{IP: net.ParseIP("192.168.1.1"), DiscoveredAt: time.Now()}},
	}
	if len(state.Hosts) > 0 {
		state.Modal = ModalState{}
		state.Transition(ScreenModal)
	}
	if state.Screen != ScreenModal {
		t.Fatal("expected ScreenModal after Enter with hosts")
	}
}

func TestTransition_EscFromModal(t *testing.T) {
	state := &UIState{Screen: ScreenModal}
	state.Modal = ModalState{}
	state.Transition(ScreenDiscovery)
	if state.Screen != ScreenDiscovery {
		t.Fatal("expected ScreenDiscovery after Esc")
	}
}

func TestTransition_InvalidBlocked(t *testing.T) {
	state := &UIState{Screen: ScreenDiscovery}
	state.Transition(ScreenSession) // invalid: Discovery -> Session not allowed
	if state.Screen != ScreenDiscovery {
		t.Fatal("invalid transition Discovery->Session should be blocked")
	}
}

func TestHandleScanEvent_Dedup(t *testing.T) {
	state := &UIState{}
	ip := net.ParseIP("10.0.0.1")
	state.HandleScanEvent(domain.ScanEvent{
		Type: domain.EventHostFound,
		Host: &domain.Host{IP: ip, Hostname: "first", DiscoveredAt: time.Now()},
	})
	state.HandleScanEvent(domain.ScanEvent{
		Type: domain.EventHostFound,
		Host: &domain.Host{IP: ip, Hostname: "updated", DiscoveredAt: time.Now()},
	})
	if len(state.Hosts) != 1 {
		t.Fatalf("expected dedup to 1 host, got %d", len(state.Hosts))
	}
	if state.Hosts[0].Hostname != "updated" {
		t.Fatalf("expected hostname 'updated', got %q", state.Hosts[0].Hostname)
	}
}

// --- Key handling tests ---

func TestDiscoveryKey_Enter(t *testing.T) {
	state := &UIState{
		Screen: ScreenDiscovery,
		Hosts:  []domain.Host{{IP: net.ParseIP("10.0.0.1"), DiscoveredAt: time.Now()}},
	}
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	handleDiscoveryKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyEnter,
		Pressed: true,
	}, state, &mockScanner{}, &ctx, &cancel, &scanCh, 20)
	if state.Screen != ScreenModal {
		t.Fatal("Enter key should transition to ScreenModal")
	}
}

func TestDiscoveryKey_Esc(t *testing.T) {
	state := &UIState{Screen: ScreenModal}
	handleModalKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyEsc,
		Pressed: true,
	}, state, framebuffer.NewMock(1280, 720), nil)
	if state.Screen != ScreenDiscovery {
		t.Fatal("Esc key should transition back to ScreenDiscovery")
	}
}

func TestDiscoveryKey_Up_KeyDown(t *testing.T) {
	hosts := make([]domain.Host, 5)
	for i := range hosts {
		hosts[i] = domain.Host{IP: net.ParseIP("10.0.0.1"), DiscoveredAt: time.Now()}
	}
	state := &UIState{Screen: ScreenDiscovery, Hosts: hosts, SelectedIdx: 2}
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	handleDiscoveryKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyUp,
		Pressed: true,
	}, state, &mockScanner{}, &ctx, &cancel, &scanCh, 20)
	if state.SelectedIdx != 1 {
		t.Fatalf("Up key should decrement selection, got %d", state.SelectedIdx)
	}
}

func TestDiscoveryKey_F5_ResetsScan(t *testing.T) {
	state := &UIState{
		Screen:       ScreenDiscovery,
		Hosts:        []domain.Host{{IP: net.ParseIP("10.0.0.1"), DiscoveredAt: time.Now()}},
		ScanDone:     true,
		ScanProgress: 1.0,
		ScanPort:     3390,
	}
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	scan := &mockScanner{}
	handleDiscoveryKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyF5,
		Pressed: true,
	}, state, scan, &ctx, &cancel, &scanCh, 20)
	if state.ScanDone {
		t.Fatal("F5 should reset ScanDone")
	}
	if state.ScanProgress != 0 {
		t.Fatal("F5 should reset ScanProgress")
	}
	if scan.port != 3390 {
		t.Fatalf("F5 scanned port %d, want the one set, 3390", scan.port)
	}
	if len(state.Hosts) != 1 {
		t.Fatalf("F5 left %d hosts, want the list up while it rescans", len(state.Hosts))
	}
}

// A rescan drops at its end what it did not find again, but not a host typed
// in or saved, and the cursor stays on the host it was on.
func TestRescan_DropsWhatItDidNotFind(t *testing.T) {
	host := func(ip string) *domain.Host { return &domain.Host{IP: net.ParseIP(ip), DiscoveredAt: time.Now()} }
	state := &UIState{Screen: ScreenDiscovery}
	state.AddHost(*host("10.0.9.9")) // saved, on another subnet
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		state.HandleScanEvent(domain.ScanEvent{Type: domain.EventHostFound, Host: host(ip)})
	}
	state.HandleScanEvent(domain.ScanEvent{Type: domain.EventScanComplete})
	state.SelectedIdx, state.ScrollOffset = 3, 3 // on 10.0.0.3

	state.Rescan()
	state.HandleScanEvent(domain.ScanEvent{Type: domain.EventHostFound, Host: host("10.0.0.3")})
	if len(state.Hosts) != 4 {
		t.Fatalf("%d hosts while rescanning, want all 4 still up", len(state.Hosts))
	}
	state.HandleScanEvent(domain.ScanEvent{Type: domain.EventHostFound, Host: host("10.0.0.4")})
	state.HandleScanEvent(domain.ScanEvent{Type: domain.EventScanComplete})

	var got []string
	for _, h := range state.Hosts {
		got = append(got, h.IP.String())
	}
	if want := "10.0.9.9 10.0.0.3 10.0.0.4"; strings.Join(got, " ") != want {
		t.Fatalf("after the rescan %v, want %s", got, want)
	}
	if sel := state.SelectedHost(); sel == nil || sel.IP.String() != "10.0.0.3" || state.ScrollOffset > state.SelectedIdx {
		t.Fatalf("cursor on %v at %d, window from %d; want it on 10.0.0.3 and in view", sel, state.SelectedIdx, state.ScrollOffset)
	}
	if v := state.VisibleHosts(20); len(v) == 0 {
		t.Fatal("nothing visible after the rescan")
	}
}

func TestModalKey_Tab(t *testing.T) {
	state := &UIState{Screen: ScreenModal, Modal: ModalState{FocusIdx: 0}}
	handleModalKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyTab,
		Pressed: true,
	}, state, framebuffer.NewMock(1280, 720), nil)
	if state.Modal.FocusIdx != 1 {
		t.Fatalf("Tab should advance focus, got %d", state.Modal.FocusIdx)
	}
}

func TestModalKey_Backspace(t *testing.T) {
	state := &UIState{
		Screen: ScreenModal,
		Modal:  ModalState{Fields: [4]string{"admin", "pass", "", ""}, FocusIdx: 0},
	}
	handleModalKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyBackspace,
		Pressed: true,
	}, state, framebuffer.NewMock(1280, 720), nil)
	if state.Modal.Fields[0] != "admi" {
		t.Fatalf("Backspace should remove last char, got %q", state.Modal.Fields[0])
	}
}

func TestModalKey_Rune(t *testing.T) {
	state := &UIState{
		Screen: ScreenModal,
		Modal:  ModalState{Fields: [4]string{"adm", "", "", ""}, FocusIdx: 0},
	}
	handleModalKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: 30,
		Rune:    'i',
		Pressed: true,
	}, state, framebuffer.NewMock(1280, 720), nil)
	if state.Modal.Fields[0] != "admi" {
		t.Fatalf("rune input should append, got %q", state.Modal.Fields[0])
	}
}

func TestRenderConnecting_NonEmpty(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{Screen: ScreenConnecting}
	renderDiscovery(back, state)
	renderConnecting(back, state)
	cx, cy := 640, 360
	px := back.RGBAAt(cx, cy)
	if px == ColorBG {
		t.Fatal("connecting overlay centre should not be plain background")
	}
}

func TestRdpErrToMessage_Timeout(t *testing.T) {
	msg := rdpErrToMessage(errTest("connection timeout after 10s"))
	if msg != "Timeout: server unreachable" {
		t.Fatalf("expected English timeout message, got %q", msg)
	}
}

func TestRdpErrToMessage_Refused(t *testing.T) {
	msg := rdpErrToMessage(errTest("connection refused"))
	if msg != "Connection refused" {
		t.Fatalf("expected English refused message, got %q", msg)
	}
}

func TestRdpErrToMessage_Auth(t *testing.T) {
	msg := rdpErrToMessage(errTest("logon failure: bad credentials"))
	if msg != "Authentication failed" {
		t.Fatalf("expected English auth message, got %q", msg)
	}
}

// Error classification must survive uppercase/mixed-case error text from the
// RDP stack, which is why rdpErrToMessage lowercases before matching.
func TestRdpErrToMessage_CaseInsensitive(t *testing.T) {
	if msg := rdpErrToMessage(errTest("RDP connect 10.0.0.1: Connection REFUSED")); msg != "Connection refused" {
		t.Fatalf("mixed-case refusal not classified, got %q", msg)
	}
}

func TestRdpErrToMessage_Localised(t *testing.T) {
	i18n.Set(i18n.LangTR)
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	if msg := rdpErrToMessage(errTest("connection refused")); msg != "Bağlantı reddedildi" {
		t.Fatalf("expected Turkish refused message, got %q", msg)
	}
}

// Unclassified errors are passed through, truncated. The truncation must not
// split a multi-byte rune.
func TestRdpErrToMessage_TruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("ü", 80)
	msg := rdpErrToMessage(errTest(long))
	if !utf8.ValidString(msg) {
		t.Fatalf("truncated message is not valid UTF-8: %q", msg)
	}
	if !strings.HasSuffix(msg, "...") {
		t.Fatalf("expected truncated message to end in ellipsis, got %q", msg)
	}
}

func TestLanguageToggle_F2(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	state := &UIState{Screen: ScreenDiscovery}
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	ev := inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: inputdev.KeyF2, Pressed: true}

	handleInput(ev, state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
		&ctx, &cancel, &scanCh, nil, 20)
	if i18n.Current() != i18n.LangTR {
		t.Fatalf("F2 should switch to Turkish, got %q", i18n.Current())
	}

	handleInput(ev, state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
		&ctx, &cancel, &scanCh, nil, 20)
	if i18n.Current() != i18n.LangEN {
		t.Fatalf("second F2 should switch back to English, got %q", i18n.Current())
	}

	// F2 must not leak into the host list navigation.
	if state.Screen != ScreenDiscovery {
		t.Fatalf("F2 should not change screen, got %v", state.Screen)
	}
}

// F2 also toggles while the credential modal is open, and must not be typed
// into the focused text field.
func TestLanguageToggle_F2_InModal(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })

	state := &UIState{Screen: ScreenModal, Modal: ModalState{FocusIdx: 0}}
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent

	handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: inputdev.KeyF2, Pressed: true},
		state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
		&ctx, &cancel, &scanCh, nil, 20)

	if i18n.Current() != i18n.LangTR {
		t.Fatalf("F2 in modal should switch language, got %q", i18n.Current())
	}
	if state.Modal.Fields[0] != "" {
		t.Fatalf("F2 should not be typed into the field, got %q", state.Modal.Fields[0])
	}
}

// F9 opens the install dialog, but only on a kiosk started from a USB stick or
// CD: on an installed one it would offer to wipe the disk it runs from. Esc
// goes back to the host list.
func TestInstall_F9(t *testing.T) {
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	key := func(state *UIState, code int) {
		handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Pressed: true},
			state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
			&ctx, &cancel, &scanCh, nil, 20)
	}

	state := &UIState{Screen: ScreenDiscovery}
	key(state, inputdev.KeyF9)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("F9 on an installed kiosk changed the screen to %v", state.Screen)
	}

	state = &UIState{Screen: ScreenDiscovery, CanInstall: true}
	key(state, inputdev.KeyF9)
	if state.Screen != ScreenInstall {
		t.Fatalf("F9 should open the install dialog, screen is %v", state.Screen)
	}
	key(state, inputdev.KeyEsc)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("Esc should leave the install dialog, screen is %v", state.Screen)
	}
}

// The wheel goes through the lists a row a notch, rolled away from the user
// up the list, and stops at their ends; not while the Wi-Fi dialog asks for a
// password.
func TestWheel_MovesThroughLists(t *testing.T) {
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	wheel := func(state *UIState, notches int) {
		handleInput(inputdev.InputEvent{Type: inputdev.EvMouseWheel, Wheel: notches},
			state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
			&ctx, &cancel, &scanCh, nil, 2)
	}

	state := &UIState{Screen: ScreenDiscovery, Hosts: []domain.Host{
		{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("10.0.0.2")}, {IP: net.ParseIP("10.0.0.3")},
	}}
	wheel(state, -1)
	if state.SelectedIdx != 1 {
		t.Fatalf("a notch towards the user should select the next server, selected %d", state.SelectedIdx)
	}
	wheel(state, -5)
	if state.SelectedIdx != 2 || state.ScrollOffset != 1 {
		t.Fatalf("the wheel should stop at the last server and scroll it into view, selected %d, offset %d",
			state.SelectedIdx, state.ScrollOffset)
	}
	wheel(state, 1)
	if state.SelectedIdx != 1 {
		t.Fatalf("a notch away from the user should select the previous server, selected %d", state.SelectedIdx)
	}

	state = &UIState{Screen: ScreenInstall, Install: InstallState{Disks: make([]install.Disk, 2)}}
	wheel(state, -3)
	if state.Install.Sel != 1 {
		t.Fatalf("the wheel should stop at the last disk, selected %d", state.Install.Sel)
	}

	state = &UIState{Screen: ScreenWifi, Wifi: WifiState{Networks: make([]wifi.Network, 3)}}
	wheel(state, -1)
	if state.Wifi.Sel != 1 {
		t.Fatalf("a notch should select the next network, selected %d", state.Wifi.Sel)
	}
	state.Wifi.Prompt = true
	wheel(state, -1)
	if state.Wifi.Sel != 1 {
		t.Fatalf("the wheel moved the selection under the password prompt, selected %d", state.Wifi.Sel)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// Anything that changes what is on screen must mark the state dirty, otherwise
// the render loop never repaints. The connect goroutine relies on Transition
// doing this — without it a failed connection leaves "Connecting..." on screen
// forever.
func TestTransition_MarksDirty(t *testing.T) {
	state := &UIState{Screen: ScreenDiscovery}
	state.Transition(ScreenModal)
	if !state.Dirty {
		t.Fatal("Transition should mark the state dirty")
	}
}

func TestHandleScanEvent_MarksDirty(t *testing.T) {
	state := &UIState{}
	state.HandleScanEvent(domain.ScanEvent{Type: domain.EventScanProgress, Scanned: 1, Total: 10})
	if !state.Dirty {
		t.Fatal("HandleScanEvent should mark the state dirty")
	}
}

func TestMoveSelection_MarksDirty(t *testing.T) {
	state := &UIState{Hosts: []domain.Host{{IP: net.ParseIP("10.0.0.1")}}}
	state.MoveSelection(1, 20)
	if !state.Dirty {
		t.Fatal("MoveSelection should mark the state dirty")
	}
}

// A rejected transition still needs a repaint: the screen it was rejected from
// may already have been redrawn over.
func TestTransition_MarksDirtyEvenWhenBlocked(t *testing.T) {
	state := &UIState{Screen: ScreenDiscovery}
	state.Transition(ScreenSession) // not a permitted transition
	if state.Screen != ScreenDiscovery {
		t.Fatal("transition should have been blocked")
	}
	if !state.Dirty {
		t.Fatal("blocked Transition should still mark the state dirty")
	}
}

// Run's select loop must not stay hot once the scan finishes.
//
// A closed channel is ready forever, so leaving the finished scan in the select
// made the loop spin at ~100% of a core for as long as the kiosk was up — and
// every scan finishes, so that was always. On the low-power CPUs this runs on,
// that starves the render tick and input handling badly enough that typing into
// the credential dialog looks like a dead keyboard.
//
// This is a CPU property, so it is measured as one: the loop is left to run and
// the process's own CPU time is compared against elapsed wall time.
func TestRun_DoesNotSpinOnceTheScanIsDone(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	fb := framebuffer.NewMock(640, 480)
	reader, err := inputdev.New("", "", fb.Width(), fb.Height())
	if err != nil {
		t.Fatalf("input reader: %v", err)
	}
	t.Cleanup(reader.Close)

	// A scanner whose channel is already closed: the state Run settles into
	// within seconds of boot.
	go Run(fb, reader, &mockScanner{}, config.Config{})

	time.Sleep(200 * time.Millisecond) // let it start and drain
	start, wall := cpuTime(t), time.Now()
	time.Sleep(700 * time.Millisecond)
	used, elapsed := cpuTime(t)-start, time.Since(wall)

	// Blocking costs ~0. Spinning costs a whole core, and more with the render
	// tick on top. Half of one core is far above the former and far below the
	// latter, so this does not turn into a flaky timing test on a loaded runner.
	if ratio := used.Seconds() / elapsed.Seconds(); ratio > 0.5 {
		t.Fatalf("the loop burned %.0f%% of a core while idle — it is spinning", ratio*100)
	}
}

func cpuTime(t *testing.T) time.Duration {
	t.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatalf("getrusage: %v", err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// Reconnecting to the same machine is the normal case at a kiosk — a dropped
// session, a reboot at the far end — and retyping the account every time is
// most of the work. The password is deliberately not part of this.
func TestOpenModalFor_PrefillsTheAccountThatWorked(t *testing.T) {
	host := domain.Host{IP: net.ParseIP("10.0.0.5")}
	state := &UIState{Hosts: []domain.Host{host}}

	// Nothing remembered yet: the host's address, cursor in the username field.
	state.OpenModalFor()
	m := &state.Modal
	if m.Fields[fieldServer] != "10.0.0.5" || m.Fields[fieldUser] != "" || m.FocusIdx != fieldUser {
		t.Fatalf("first visit gave server %q user %q at focus %d, want the address and an empty username field",
			m.Fields[fieldServer], m.Fields[fieldUser], m.FocusIdx)
	}

	state.RememberCredential(host.AddrRDP(), "administrator", "CORP")
	state.OpenModalFor()

	if m.Fields[fieldUser] != "administrator" {
		t.Errorf("username = %q, want it filled in", m.Fields[fieldUser])
	}
	if !m.ShowDomain || m.Fields[fieldDomain] != "CORP" {
		t.Errorf("domain = %q (shown %v), want CORP shown", m.Fields[fieldDomain], m.ShowDomain)
	}
	if m.Fields[fieldPassword] != "" {
		t.Fatalf("the password was carried over as %q — it must never be stored", m.Fields[fieldPassword])
	}
	if m.FocusIdx != fieldPassword {
		t.Errorf("focus is on field %d, want the password field so the user can just type", m.FocusIdx)
	}
}

// F7 opens the dialog for a server typed in by hand, and Enter does the same
// when the scan found nothing to pick.
func TestManualAddress(t *testing.T) {
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	for _, code := range []int{inputdev.KeyF7, inputdev.KeyEnter} {
		state := &UIState{Screen: ScreenDiscovery}
		handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Pressed: true},
			state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
			&ctx, &cancel, &scanCh, nil, 20)
		if state.Screen != ScreenModal || state.Modal.FocusIdx != fieldServer || state.Modal.Fields[fieldServer] != "" {
			t.Fatalf("key %d: screen %v focus %d server %q, want an empty dialog with the cursor in the server field",
				code, state.Screen, state.Modal.FocusIdx, state.Modal.Fields[fieldServer])
		}
	}
}

// The domain field is an option: hidden until F6, and what was typed into it
// counts only while it is shown.
func TestModalKey_F6TogglesDomain(t *testing.T) {
	state := &UIState{Screen: ScreenModal}
	m := &state.Modal
	key := func(code int, r rune) {
		handleModalKey(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Rune: r, Pressed: true},
			state, framebuffer.NewMock(1280, 720), nil)
	}

	if m.ShowDomain || m.fieldCount() != 3 {
		t.Fatal("the domain field should start hidden")
	}
	key(inputdev.KeyF6, 0)
	if !m.ShowDomain || m.FocusIdx != fieldDomain {
		t.Fatalf("F6 should show the domain field and focus it (shown %v, focus %d)", m.ShowDomain, m.FocusIdx)
	}
	key(30, 'C')
	if m.domain() != "C" {
		t.Fatalf("typing into the domain field gave %q", m.domain())
	}

	key(inputdev.KeyTab, 0)
	if m.FocusIdx != m.focusConnect() {
		t.Fatalf("Tab from the domain field focused %d, want the connect button", m.FocusIdx)
	}
	key(inputdev.KeyF6, 0)
	if m.ShowDomain || m.FocusIdx != m.focusConnect() {
		t.Fatalf("hiding the field should keep the connect button focused (focus %d)", m.FocusIdx)
	}
	if m.domain() != "" {
		t.Fatalf("a hidden domain field still gave %q", m.domain())
	}
}

// The hint is per host: the account for one machine must not be offered for
// another.
func TestOpenModalFor_IsPerHost(t *testing.T) {
	a := domain.Host{IP: net.ParseIP("10.0.0.5")}
	b := domain.Host{IP: net.ParseIP("10.0.0.6")}
	state := &UIState{Hosts: []domain.Host{a, b}}

	state.RememberCredential(a.AddrRDP(), "administrator", "")

	state.SelectedIdx = 1
	state.OpenModalFor()
	if state.Modal.Fields[fieldUser] != "" {
		t.Fatalf("host b offered %q, which belongs to host a", state.Modal.Fields[fieldUser])
	}

	state.SelectedIdx = 0
	state.OpenModalFor()
	if state.Modal.Fields[fieldUser] != "administrator" {
		t.Errorf("host a lost its remembered account")
	}
}

// A blank username is not worth remembering, and would otherwise move the
// cursor to the password field with nothing above it.
func TestRememberCredential_IgnoresEmpty(t *testing.T) {
	host := domain.Host{IP: net.ParseIP("10.0.0.5")}
	state := &UIState{Hosts: []domain.Host{host}}

	state.RememberCredential(host.AddrRDP(), "", "CORP")
	state.OpenModalFor()

	if state.Modal.FocusIdx != fieldUser {
		t.Errorf("focus is on field %d, want the username field", state.Modal.FocusIdx)
	}
}

// An installed kiosk keeps the servers it connected to, most recent first, with
// the account but never the password, and lists them on the next boot.
func TestSavedConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "simpleclient", "hosts")
	a, _ := domain.ParseIPAddress("10.0.0.5")
	b, _ := domain.ParseIPAddress("10.9.9.9:3390")
	b.Hostname = "vm-b"

	s := &UIState{SavePath: path}
	s.saveConnection(a, "ali", "")
	s.saveConnection(b, "veli", "CORP")
	s.saveConnection(a, "ali", "") // again: moves to the front, not duplicated

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "10.0.0.5\t\tali\t\n10.9.9.9:3390\tvm-b\tveli\tCORP\n"
	if string(data) != want {
		t.Fatalf("saved file:\n%q\nwant\n%q", data, want)
	}

	next := &UIState{}
	next.loadSaved(path)
	if len(next.Hosts) != 2 || !next.Hosts[1].Same(b) || next.Hosts[1].Hostname != "vm-b" {
		t.Fatalf("loaded hosts %+v", next.Hosts)
	}
	next.SelectedIdx = 1
	next.OpenModalFor()
	if next.Modal.Fields[fieldServer] != "10.9.9.9:3390" || next.Modal.Fields[fieldUser] != "veli" ||
		next.Modal.Fields[fieldDomain] != "CORP" {
		t.Fatalf("dialog after reboot: %q", next.Modal.Fields)
	}
}

// rdphost=<ip> is the one door past the scan-your-own-subnet boundary, so it
// must land in the list exactly once and reject garbage rather than show a row
// that can never connect.
func TestAddStaticHost(t *testing.T) {
	state := &UIState{}
	if !state.AddStaticHost("10.7.7.107") {
		t.Fatal("a valid address was rejected")
	}
	if state.AddStaticHost("10.7.7.107"); len(state.Hosts) != 1 {
		t.Fatalf("adding the same address twice left %d rows", len(state.Hosts))
	}
	if state.AddStaticHost("not-an-ip") {
		t.Error("garbage was accepted")
	}
	if got := state.Hosts[0].AddrRDP(); got != "10.7.7.107:3389" {
		t.Errorf("static host dials %q", got)
	}
}

// "Disconnected" is a passing message: it goes away by itself, and a message
// set without a time — a bad rdphost= — stays.
func TestFlash_Expires(t *testing.T) {
	now := time.Now()
	state := &UIState{}
	state.flash(i18n.T(i18n.Disconnected), now)
	state.Dirty = false

	state.expireBanner(now.Add(bannerFor - time.Millisecond))
	if state.ErrorMsg == "" || state.Dirty {
		t.Fatal("the banner went before its time")
	}
	state.expireBanner(now.Add(bannerFor))
	if state.ErrorMsg != "" || !state.Dirty {
		t.Fatalf("the banner is still up (%q), or the screen was not redrawn", state.ErrorMsg)
	}

	state = &UIState{ErrorMsg: "rdphost: x?"}
	state.expireBanner(now.Add(time.Hour))
	if state.ErrorMsg == "" {
		t.Error("a banner with no time was taken down")
	}
}

// One click on a server picks it; a second on it soon after opens its
// dialog. A second click on another server, or too late, only picks again.
func TestDiscoveryClick_DoubleOpensDialog(t *testing.T) {
	fb := framebuffer.NewMock(1280, 800)
	clock := time.Unix(1000, 0)
	old := clickClock
	clickClock = func() time.Time { return clock }
	t.Cleanup(func() { clickClock = old })

	state := &UIState{Screen: ScreenDiscovery, Hosts: []domain.Host{
		{IP: net.IPv4(10, 0, 0, 1)}, {IP: net.IPv4(10, 0, 0, 2)}, {IP: net.IPv4(10, 0, 0, 3)},
	}}
	l := layoutDiscoveryFor(fb.Bounds(), state.Hosts)
	click := func(row int, after time.Duration) {
		clock = clock.Add(after)
		r := l.rowRect(row)
		handleMouseClick(r.Min.X+20, r.Min.Y+r.Dy()/2, state, fb, nil)
	}

	click(1, 0)
	if state.Screen != ScreenDiscovery || state.SelectedIdx != 1 {
		t.Fatalf("one click: screen %v selected %d, want row 1 picked on the list", state.Screen, state.SelectedIdx)
	}
	click(2, 100*time.Millisecond)
	if state.Screen != ScreenDiscovery || state.SelectedIdx != 2 {
		t.Fatalf("a click on another row: screen %v selected %d, want row 2 picked", state.Screen, state.SelectedIdx)
	}
	click(2, time.Second)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("a second click a second later opened the dialog")
	}
	click(2, 200*time.Millisecond)
	if state.Screen != ScreenModal || state.SelectedIdx != 2 {
		t.Fatalf("double click: screen %v selected %d, want row 2's dialog", state.Screen, state.SelectedIdx)
	}
}
