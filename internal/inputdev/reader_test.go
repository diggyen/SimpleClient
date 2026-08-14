package inputdev

import (
	"strings"
	"testing"
)

func TestKeycodeToRune_Unshifted(t *testing.T) {
	r := KeycodeToRune(30, false) // KEY_A
	if r != 'a' {
		t.Fatalf("expected 'a', got %q", r)
	}
}

func TestKeycodeToRune_Shifted(t *testing.T) {
	r := KeycodeToRune(30, true) // KEY_A shifted
	if r != 'A' {
		t.Fatalf("expected 'A', got %q", r)
	}
}

func TestKeycodeToRune_Special(t *testing.T) {
	r := KeycodeToRune(KeyEnter, false)
	if r != 0 {
		t.Fatalf("Enter should return rune 0, got %q", r)
	}
}

func TestClampMouse(t *testing.T) {
	cases := []struct{ v, max, want int }{
		{-5, 1280, 0},
		{0, 1280, 0},
		{640, 1280, 640},
		{1279, 1280, 1279},
		{1280, 1280, 1279},
		{9999, 1280, 1279},
	}
	for _, c := range cases {
		got := clampMouse(c.v, c.max)
		if got != c.want {
			t.Errorf("clampMouse(%d, %d) = %d, want %d", c.v, c.max, got, c.want)
		}
	}
}

// Real content from a QEMU guest. The mouse block is the interesting one: evdev
// is its only handler, so the node is glued to "Handlers=". Splitting the whole
// line on whitespace finds event0 for the keyboard (which lists sysrq and kbd
// first) but misses event1 entirely, which left the kiosk with no pointer.
const procInputDevices = `I: Bus=0011 Vendor=0001 Product=0001 Version=ab41
N: Name="AT Translated Set 2 keyboard"
P: Phys=isa0060/serio0/input0
S: Sysfs=/devices/platform/i8042/serio0/input/input0
U: Uniq=
H: Handlers=sysrq kbd event0
B: PROP=0
B: EV=120013

I: Bus=0011 Vendor=0002 Product=0006 Version=0000
N: Name="ImExPS/2 Generic Explorer Mouse"
P: Phys=isa0060/serio1/input0
S: Sysfs=/devices/platform/i8042/serio1/input/input1
U: Uniq=
H: Handlers=event1
B: PROP=1
B: EV=7
`

func TestFindDevice_Keyboard(t *testing.T) {
	got, err := findDevice(strings.NewReader(procInputDevices), "keyboard")
	if err != nil {
		t.Fatalf("findDevice(keyboard): %v", err)
	}
	if got != "/dev/input/event0" {
		t.Errorf("keyboard = %q, want /dev/input/event0", got)
	}
}

func TestFindDevice_MouseWithEvdevOnlyHandler(t *testing.T) {
	got, err := findDevice(strings.NewReader(procInputDevices), "mouse")
	if err != nil {
		t.Fatalf("findDevice(mouse): %v", err)
	}
	if got != "/dev/input/event1" {
		t.Errorf("mouse = %q, want /dev/input/event1", got)
	}
}

// A mouse that also has the legacy mousedev handler must still resolve to its
// evdev node, not to mouse0.
func TestFindDevice_MouseWithMousedevHandler(t *testing.T) {
	const devices = `I: Bus=0003 Vendor=046d Product=c52b Version=0111
N: Name="Logitech USB Receiver Mouse"
H: Handlers=mouse0 event4
B: EV=17
`
	got, err := findDevice(strings.NewReader(devices), "mouse")
	if err != nil {
		t.Fatalf("findDevice(mouse): %v", err)
	}
	if got != "/dev/input/event4" {
		t.Errorf("mouse = %q, want /dev/input/event4", got)
	}
}

func TestFindDevice_Touchpad(t *testing.T) {
	const devices = `I: Bus=0018 Vendor=06cb Product=7e7e Version=0100
N: Name="SynPS/2 Synaptics TouchPad"
H: Handlers=event6
B: EV=b
`
	got, err := findDevice(strings.NewReader(devices), "mouse")
	if err != nil {
		t.Fatalf("findDevice(touchpad): %v", err)
	}
	if got != "/dev/input/event6" {
		t.Errorf("touchpad = %q, want /dev/input/event6", got)
	}
}

func TestFindDevice_NotFound(t *testing.T) {
	const devices = `I: Bus=0019 Vendor=0000 Product=0001 Version=0000
N: Name="Power Button"
H: Handlers=kbd event0
B: EV=3
`
	if _, err := findDevice(strings.NewReader(devices), "mouse"); err == nil {
		t.Fatal("expected an error when no mouse is present")
	}
}

// A block with no N: line must not inherit the previous block's name.
func TestFindDevice_NameDoesNotLeakBetweenBlocks(t *testing.T) {
	const devices = `I: Bus=0011 Vendor=0002 Product=0006 Version=0000
N: Name="ImExPS/2 Generic Explorer Mouse"
H: Handlers=event1

I: Bus=0000 Vendor=0000 Product=0000 Version=0000
H: Handlers=event2
`
	got, err := findDevice(strings.NewReader(devices), "mouse")
	if err != nil {
		t.Fatalf("findDevice(mouse): %v", err)
	}
	if got != "/dev/input/event1" {
		t.Errorf("mouse = %q, want /dev/input/event1", got)
	}
}
