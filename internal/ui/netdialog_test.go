package ui

import (
	"testing"

	"github.com/diggyen/SimpleClient/internal/inputdev"
)

func netKey(state *UIState, code int, r rune) {
	handleNetKey(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Rune: r, Pressed: true}, state)
}

// With DHCP chosen the fields do not apply, and Tab skips them to the port;
// switching to manual makes them reachable and editable.
func TestNetDialog_FocusAndEditing(t *testing.T) {
	state := &UIState{Screen: ScreenNetwork}
	n := &state.Net

	netKey(state, inputdev.KeyTab, 0)
	if n.Focus != netFocusPort {
		t.Fatalf("with DHCP, Tab from the switch went to %d, want the port", n.Focus)
	}
	netKey(state, inputdev.KeyTab, 0)
	if n.Focus != netFocusSave {
		t.Fatalf("with DHCP, Tab from the port went to %d, want the save button", n.Focus)
	}
	netKey(state, inputdev.KeyUp, 0)
	netKey(state, inputdev.KeyUp, 0)
	if n.Focus != netFocusMode {
		t.Fatalf("Up from save past the port went to %d, want the switch", n.Focus)
	}

	netKey(state, inputdev.KeySpace, ' ')
	if !n.Static {
		t.Fatal("Space on the switch should choose manual")
	}
	netKey(state, inputdev.KeyTab, 0)
	if n.field() != netAddress {
		t.Fatalf("with manual, Tab went to %d, want the address field", n.Focus)
	}
	for _, r := range "10.0.0.7" {
		netKey(state, 0, r)
	}
	netKey(state, inputdev.KeyBackspace, 0)
	netKey(state, 0, '8')
	if n.Fields[netAddress] != "10.0.0.8" {
		t.Fatalf("address field holds %q", n.Fields[netAddress])
	}
	netKey(state, inputdev.KeyEsc, 0)
	if state.Screen != ScreenDiscovery {
		t.Fatal("Esc should close the dialog")
	}
}

// A field the form check rejects gets the focus and its own message, and
// nothing is applied.
func TestNetDialog_RejectsBadInput(t *testing.T) {
	state := &UIState{Screen: ScreenNetwork}
	n := &state.Net
	n.Static = true
	n.Fields = [netFields]string{"192.168.1.50", "255.255.255.0", "10.0.0.1", "", ""}
	n.Focus = netFocusSave

	netKey(state, inputdev.KeyEnter, 0)
	if n.Busy || !n.Err || n.field() != netGateway {
		t.Fatalf("busy %v err %v focus %d (%q), want the gateway field flagged", n.Busy, n.Err, n.Focus, n.Msg)
	}
}

// The port takes digits only, five at most; one out of range is flagged and
// nothing is applied.
func TestNetDialog_Port(t *testing.T) {
	state := &UIState{Screen: ScreenNetwork}
	n := &state.Net
	n.Port = "3389"
	n.Focus = netFocusPort

	for range 4 {
		netKey(state, inputdev.KeyBackspace, 0)
	}
	for _, r := range "3a3-9.0123" {
		netKey(state, 0, r)
	}
	if n.Port != "33901" {
		t.Fatalf("port field holds %q, want 33901", n.Port)
	}

	n.Port = "70000"
	netKey(state, inputdev.KeyEnter, 0)
	if n.Busy || !n.Err || n.Focus != netFocusPort {
		t.Fatalf("busy %v err %v focus %d (%q), want the port flagged", n.Busy, n.Err, n.Focus, n.Msg)
	}
}
