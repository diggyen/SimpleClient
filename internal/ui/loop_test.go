package ui

import (
	"context"
	"image"
	"net"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
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
	if msg != "Zaman aşımı: sunucuya ulaşılamıyor" {
		t.Fatalf("expected Turkish timeout message, got %q", msg)
	}
}

func TestRdpErrToMessage_Refused(t *testing.T) {
	msg := rdpErrToMessage(errTest("connection refused"))
	if msg != "Bağlantı reddedildi" {
		t.Fatalf("expected Turkish refused message, got %q", msg)
	}
}

func TestRdpErrToMessage_Auth(t *testing.T) {
	msg := rdpErrToMessage(errTest("logon failure: bad credentials"))
	if msg != "Kimlik doğrulama başarısız" {
		t.Fatalf("expected Turkish auth message, got %q", msg)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
