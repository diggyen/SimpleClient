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

// A password typed on the number pad used to produce nothing while the rest of
// the keyboard worked, which reads as a field refusing input rather than as a
// missing mapping.
func TestKeycodeToRune_NumericKeypad(t *testing.T) {
	pad := map[int]rune{
		71: '7', 72: '8', 73: '9',
		75: '4', 76: '5', 77: '6',
		79: '1', 80: '2', 81: '3',
		82: '0', 83: '.',
	}
	for code, want := range pad {
		for _, shifted := range []bool{false, true} {
			if got := KeycodeToRune(code, shifted); got != want {
				t.Errorf("keycode %d (shift=%v) = %q, want %q", code, shifted, got, want)
			}
		}
	}
}

// Every letter must produce a rune in both cases: the credential dialog is the
// only place text is typed, and a gap here is invisible until someone cannot
// log in.
func TestKeycodeToRune_CoversTheAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for code := 0; code < 128; code++ {
		lower := KeycodeToRune(code, false)
		upper := KeycodeToRune(code, true)
		if lower >= 'a' && lower <= 'z' {
			seen[lower] = true
			if upper != lower-32 {
				t.Errorf("keycode %d: shifted %q is not the capital of %q", code, upper, lower)
			}
		}
	}
	for c := 'a'; c <= 'z'; c++ {
		if !seen[c] {
			t.Errorf("no keycode produces %q", c)
		}
	}
}

// Turkish Q is the layout that matters here: choosing Türkçe selects it, and
// the dotted/dotless i pair is the part everyone gets wrong. The key in the US
// "i" position types ı; the key in the US apostrophe position types i.
func TestKeycodeToRuneIn_TurkishQ(t *testing.T) {
	cases := []struct {
		code             int
		unshift, shifted rune
	}{
		{23, 'ı', 'I'},
		{40, 'i', 'İ'},
		{26, 'ğ', 'Ğ'},
		{27, 'ü', 'Ü'},
		{39, 'ş', 'Ş'},
		{51, 'ö', 'Ö'},
		{52, 'ç', 'Ç'},
		{30, 'a', 'A'}, // unchanged from US, and must stay so
		{57, ' ', ' '},
	}
	for _, c := range cases {
		if got := KeycodeToRuneIn(LayoutTRQ, c.code, false); got != c.unshift {
			t.Errorf("TR-Q keycode %d = %q, want %q", c.code, got, c.unshift)
		}
		if got := KeycodeToRuneIn(LayoutTRQ, c.code, true); got != c.shifted {
			t.Errorf("TR-Q keycode %d shifted = %q, want %q", c.code, got, c.shifted)
		}
	}
}

// F moves the letters themselves, so a few spot checks that would be wrong on
// both US and Q.
func TestKeycodeToRuneIn_TurkishF(t *testing.T) {
	for code, want := range map[int]rune{16: 'f', 17: 'g', 30: 'u', 33: 'a', 44: 'j'} {
		if got := KeycodeToRuneIn(LayoutTRF, code, false); got != want {
			t.Errorf("TR-F keycode %d = %q, want %q", code, got, want)
		}
	}
}

// Every layout must still produce something for the keys a credential is made
// of. A layout that silently drops the digits or the keypad would look like the
// field refusing input, which is the bug this project has already shipped once.
func TestLayouts_CoverDigitsAndKeypad(t *testing.T) {
	digits := []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	keypad := []int{71, 72, 73, 75, 76, 77, 79, 80, 81, 82}

	for _, l := range LayoutOrder {
		for _, code := range append(append([]int{}, digits...), keypad...) {
			if got := KeycodeToRuneIn(l, code, false); got == 0 {
				t.Errorf("layout %s: keycode %d produces nothing", l, code)
			}
		}
		// And the alphabet, in whatever positions this layout puts it.
		seen := map[rune]bool{}
		for code := 0; code < 128; code++ {
			if r := KeycodeToRuneIn(l, code, false); r >= 'a' && r <= 'z' {
				seen[r] = true
			}
		}
		for c := 'a'; c <= 'z'; c++ {
			if !seen[c] {
				t.Errorf("layout %s: no key produces %q", l, c)
			}
		}
	}
}

// The language picker sets the layout, so Türkçe must not leave a Turkish user
// on a US keymap.
func TestLayoutForLanguage(t *testing.T) {
	if got := LayoutForLanguage("tr"); got != LayoutTRQ {
		t.Errorf("tr -> %s, want %s", got, LayoutTRQ)
	}
	if got := LayoutForLanguage("en"); got != LayoutUS {
		t.Errorf("en -> %s, want %s", got, LayoutUS)
	}
}

func TestNextLayout_CyclesAndReturns(t *testing.T) {
	t.Cleanup(func() { SetLayout(LayoutUS) })
	SetLayout(LayoutUS)
	seen := map[Layout]bool{}
	for range LayoutOrder {
		seen[NextLayout()] = true
	}
	if len(seen) != len(LayoutOrder) {
		t.Fatalf("cycling visited %d of %d layouts", len(seen), len(LayoutOrder))
	}
	if CurrentLayout() != LayoutUS {
		t.Errorf("a full cycle ended on %s, want back at %s", CurrentLayout(), LayoutUS)
	}
}
