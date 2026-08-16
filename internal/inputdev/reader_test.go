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

// Real content from a QEMU guest, with the capability bitmaps the kernel
// actually prints. The mouse block is the interesting one for parsing: evdev is
// its only handler, so the node is glued to "Handlers=". Splitting the whole
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
B: KEY=402000002 3803078f800d001 feffffdfffefffff fffffffffffffffe

I: Bus=0011 Vendor=0002 Product=0006 Version=0000
N: Name="ImExPS/2 Generic Explorer Mouse"
P: Phys=isa0060/serio1/input0
S: Sysfs=/devices/platform/i8042/serio1/input/input1
U: Uniq=
H: Handlers=event1
B: PROP=1
B: EV=7
B: KEY=1f0000 0 0 0 0
B: REL=143
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

// The bug this replaced: detection matched the word "keyboard" in the device
// name. The name is the USB product string, and most real keyboards do not put
// that word in it — so on hardware the kiosk came up with a working mouse and a
// dead keyboard. Every name here is from a real device and none of them would
// have matched.
func TestFindDevice_KeyboardWithoutTheWordInItsName(t *testing.T) {
	for _, name := range []string{
		"Logitech USB Receiver",
		"SONiX USB DEVICE",
		"HID 046a:0011",
		"Compx 2.4G Wireless Receiver",
	} {
		devices := `I: Bus=0003 Vendor=046d Product=c52b Version=0111
N: Name="` + name + `"
H: Handlers=sysrq kbd event3
B: EV=120013
B: KEY=1000000000007 ff9f207ac14057ff febeffdfffefffff fffffffffffffffe
`
		got, err := findDevice(strings.NewReader(devices), "keyboard")
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if got != "/dev/input/event3" {
			t.Errorf("%q: keyboard = %q, want /dev/input/event3", name, got)
		}
	}
}

// The other half of the same problem. A power button, a lid switch and an ACPI
// video bus all carry EV_KEY and all get a "kbd" handler, and on real hardware
// they enumerate ahead of the keyboard — so anything keying off the handler
// list picks the power button and the kiosk is still dead. Only the last block
// here can type.
func TestFindDevice_SkipsButtonsAndPicksTheRealKeyboard(t *testing.T) {
	const devices = `I: Bus=0019 Vendor=0000 Product=0001 Version=0000
N: Name="Power Button"
H: Handlers=kbd event0
B: EV=3
B: KEY=10000000000000 0

I: Bus=0019 Vendor=0000 Product=0006 Version=0000
N: Name="Video Bus"
H: Handlers=kbd event2
B: EV=3
B: KEY=3e000b00000000 0 0 0

I: Bus=0003 Vendor=413c Product=2113 Version=0111
N: Name="Dell KB216 Wired Keyboard"
H: Handlers=sysrq kbd event4
B: EV=120013
B: KEY=1000000000007 ff9f207ac14057ff febeffdfffefffff fffffffffffffffe
`
	got, err := findDevice(strings.NewReader(devices), "keyboard")
	if err != nil {
		t.Fatalf("findDevice(keyboard): %v", err)
	}
	if got != "/dev/input/event4" {
		t.Errorf("keyboard = %q, want /dev/input/event4 (the only block that can type)", got)
	}
}

// A mouse that also has the legacy mousedev handler must still resolve to its
// evdev node, not to mouse0.
func TestFindDevice_MouseWithMousedevHandler(t *testing.T) {
	const devices = `I: Bus=0003 Vendor=046d Product=c52b Version=0111
N: Name="Logitech USB Receiver"
H: Handlers=mouse0 event4
B: EV=17
B: KEY=70000 0 0 0 0
B: REL=903
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
B: KEY=e520 10000 0 0 0 0
B: ABS=660800011000003
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
B: KEY=10000000000000 0
`
	if _, err := findDevice(strings.NewReader(devices), "mouse"); err == nil {
		t.Fatal("expected an error when no mouse is present")
	}
	if _, err := findDevice(strings.NewReader(devices), "keyboard"); err == nil {
		t.Fatal("a power button is not a keyboard")
	}
}

// The kernel prints capability bitmaps highest-order group first, so the last
// field holds bits 0-63. Getting the order backwards puts every bit in the
// wrong place, which is invisible until a real device has more than 64 keys.
func TestParseBitmap_GroupOrder(t *testing.T) {
	b := parseBitmap("3 8000000000000000 1")
	for _, c := range []struct {
		bit  int
		want bool
	}{
		{0, true},    // last field, bit 0
		{1, false},   //
		{127, true},  // middle field, high bit
		{128, true},  // first field
		{129, true},  //
		{130, false}, //
		{4096, false},
	} {
		if got := b.has(c.bit); got != c.want {
			t.Errorf("bit %d = %v, want %v", c.bit, got, c.want)
		}
	}
}
