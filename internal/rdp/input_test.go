package rdp

import "testing"

// grdp's mouse button numbering is not the one its parameter name suggests, not
// evdev's, and not RDP's: its switch reads 0 as left, 2 as right and 1 as
// middle. Passing this package's 1 for left therefore landed on BUTTON3, so
// every left click reached the server as a middle click — windows highlighted
// under the pointer and nothing ever activated, which is what "connected but I
// cannot control it" looked like. Passing 3 for middle fell through grdp's
// default and went out as a bare pointer move.
func TestGrdpButton(t *testing.T) {
	cases := map[int]int{
		1: 0, // left
		2: 2, // right
		3: 1, // middle
	}
	for ours, want := range cases {
		if got := grdpButton(ours); got != want {
			t.Errorf("grdpButton(%d) = %d, want %d", ours, got, want)
		}
	}

	// Anything unexpected has to be a left click rather than grdp's default,
	// which is a move carrying no button at all and silently loses the event.
	for _, odd := range []int{0, 4, -1} {
		if got := grdpButton(odd); got != 0 {
			t.Errorf("grdpButton(%d) = %d, want it to fall back to left (0)", odd, got)
		}
	}
}

// Every key the credential dialog and a session need has to resolve to a
// scancode; a zero means SendKey drops the event before it reaches grdp.
func TestLinuxToRDPScanCode_CoversTheUsualKeys(t *testing.T) {
	for _, kc := range []int{
		1,          // esc
		14,         // backspace
		15,         // tab
		28,         // enter
		29,         // left ctrl
		42,         // left shift
		56,         // left alt
		57,         // space
		30, 44, 50, // a z m
		2, 11, // 1 0
	} {
		if sc := linuxToRDPScanCode(kc); sc == 0 {
			t.Errorf("linux keycode %d has no scancode, so it can never reach a session", kc)
		}
	}
}
