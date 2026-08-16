package ui

import (
	"context"
	"image"
	"net"
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
)

// mockScanner implements domain.Scanner for loop tests.
type mockScanner struct {
	events []domain.ScanEvent
}

func (m *mockScanner) Start(_ context.Context, _ []string) <-chan domain.ScanEvent {
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
	}
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	handleDiscoveryKey(inputdev.InputEvent{
		Type:    inputdev.EvKey,
		KeyCode: inputdev.KeyF5,
		Pressed: true,
	}, state, &mockScanner{}, &ctx, &cancel, &scanCh, 20)
	if state.ScanDone {
		t.Fatal("F5 should reset ScanDone")
	}
	if state.ScanProgress != 0 {
		t.Fatal("F5 should reset ScanProgress")
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
		Modal:  ModalState{Fields: [3]string{"admin", "pass", ""}, FocusIdx: 0},
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
		Modal:  ModalState{Fields: [3]string{"adm", "", ""}, FocusIdx: 0},
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
