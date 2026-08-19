package inputdev

import "sync/atomic"

// Special key constants (Linux keycodes).
const (
	KeyEsc        = 1
	KeyBackspace  = 14
	KeyTab        = 15
	KeyEnter      = 28
	KeyCtrl       = 29
	KeyA          = 30
	KeyZ          = 44
	KeyLeftShift  = 42
	KeyRightShift = 54
	KeyAlt        = 56
	KeySpace      = 57
	KeyF1         = 59
	KeyF2         = 60
	KeyF3         = 61
	KeyF5         = 63
	KeyF10        = 68
	KeyUp         = 103
	KeyPageUp     = 104
	KeyLeft       = 105
	KeyRight      = 106
	KeyDown       = 108
	KeyPageDown   = 109
	KeyEnd        = 107
	KeyHome       = 102

	// Mouse button keycodes (EV_KEY events from mouse device)
	BtnLeft   = 0x110
	BtnRight  = 0x111
	BtnMiddle = 0x112
)

// Layout identifies a keyboard layout. Only the credential dialog is affected:
// inside an RDP session the raw keycode is forwarded and the remote machine
// applies its own layout, which is the behaviour anyone connecting to a Windows
// desktop expects.
type Layout string

const (
	// LayoutUS is the US/QWERTY layout and the startup default.
	LayoutUS Layout = "us"
	// LayoutTRQ is the Turkish Q layout — the common one.
	LayoutTRQ Layout = "tr-q"
	// LayoutTRF is the Turkish F layout.
	LayoutTRF Layout = "tr-f"
)

// LayoutOrder is the cycle sequence used by NextLayout.
var LayoutOrder = []Layout{LayoutUS, LayoutTRQ, LayoutTRF}

// Label returns the short name shown on screen.
func (l Layout) Label() string {
	switch l {
	case LayoutTRQ:
		return "TR-Q"
	case LayoutTRF:
		return "TR-F"
	default:
		return "US"
	}
}

// LayoutForLanguage returns the layout that goes with a UI language code, so
// choosing Türkçe does not leave a Turkish user typing on a US keymap.
func LayoutForLanguage(lang string) Layout {
	if lang == "tr" {
		return LayoutTRQ
	}
	return LayoutUS
}

// activeLayout is read from the keyboard goroutine and written from the input
// handler, so it goes through atomic.Pointer rather than a plain variable.
var activeLayout atomic.Pointer[Layout]

func init() { SetLayout(LayoutUS) }

// SetLayout switches the active layout. An unknown layout falls back to US.
func SetLayout(l Layout) {
	if _, ok := layouts[l]; !ok {
		l = LayoutUS
	}
	activeLayout.Store(&l)
}

// CurrentLayout returns the active layout.
func CurrentLayout() Layout {
	if p := activeLayout.Load(); p != nil {
		return *p
	}
	return LayoutUS
}

// NextLayout advances to the next layout in the cycle and returns it.
func NextLayout() Layout {
	cur := CurrentLayout()
	for i, l := range LayoutOrder {
		if l == cur {
			next := LayoutOrder[(i+1)%len(LayoutOrder)]
			SetLayout(next)
			return next
		}
	}
	SetLayout(LayoutUS)
	return LayoutUS
}

// usKeycodeToRune maps Linux keycode → [unshifted, shifted] runes.
// 0 rune means no printable character (special key).
var usKeycodeToRune = map[int][2]rune{
	2:  {'1', '!'},
	3:  {'2', '@'},
	4:  {'3', '#'},
	5:  {'4', '$'},
	6:  {'5', '%'},
	7:  {'6', '^'},
	8:  {'7', '&'},
	9:  {'8', '*'},
	10: {'9', '('},
	11: {'0', ')'},
	12: {'-', '_'},
	13: {'=', '+'},
	16: {'q', 'Q'},
	17: {'w', 'W'},
	18: {'e', 'E'},
	19: {'r', 'R'},
	20: {'t', 'T'},
	21: {'y', 'Y'},
	22: {'u', 'U'},
	23: {'i', 'I'},
	24: {'o', 'O'},
	25: {'p', 'P'},
	26: {'[', '{'},
	27: {']', '}'},
	30: {'a', 'A'},
	31: {'s', 'S'},
	32: {'d', 'D'},
	33: {'f', 'F'},
	34: {'g', 'G'},
	35: {'h', 'H'},
	36: {'j', 'J'},
	37: {'k', 'K'},
	38: {'l', 'L'},
	39: {';', ':'},
	40: {'\'', '"'},
	41: {'`', '~'},
	43: {'\\', '|'},
	44: {'z', 'Z'},
	45: {'x', 'X'},
	46: {'c', 'C'},
	47: {'v', 'V'},
	48: {'b', 'B'},
	49: {'n', 'N'},
	50: {'m', 'M'},
	51: {',', '<'},
	52: {'.', '>'},
	53: {'/', '?'},
	57: {' ', ' '},

	// The numeric keypad. Without these a password typed on the number pad
	// produces nothing at all, while the rest of the keyboard works — which
	// looks like the field is refusing input rather than like a missing
	// mapping. Num Lock is not tracked: the kiosk has no cursor keys to lose,
	// so the digits are always digits.
	71: {'7', '7'},
	72: {'8', '8'},
	73: {'9', '9'},
	74: {'-', '-'},
	75: {'4', '4'},
	76: {'5', '5'},
	77: {'6', '6'},
	78: {'+', '+'},
	79: {'1', '1'},
	80: {'2', '2'},
	81: {'3', '3'},
	82: {'0', '0'},
	83: {'.', '.'},
	98: {'/', '/'},
	55: {'*', '*'},

	// The extra key European layouts have between left shift and Z.
	86: {'\\', '|'},
}

// trQOverrides are the keys where Turkish Q differs from US. Everything not
// listed here — the letters a-z in their QWERTY positions, the digits, the
// keypad — is shared, so only the differences are carried.
//
// The dotted/dotless i pair is the one to get right: the key in the US "i"
// position types ı, and the key in the US apostrophe position types i.
var trQOverrides = map[int][2]rune{
	41: {'"', 'é'},
	2:  {'1', '!'},
	3:  {'2', '\''},
	4:  {'3', '^'},
	5:  {'4', '+'},
	6:  {'5', '%'},
	7:  {'6', '&'},
	8:  {'7', '/'},
	9:  {'8', '('},
	10: {'9', ')'},
	11: {'0', '='},
	12: {'*', '?'},
	13: {'-', '_'},

	23: {'ı', 'I'},
	26: {'ğ', 'Ğ'},
	27: {'ü', 'Ü'},

	39: {'ş', 'Ş'},
	40: {'i', 'İ'},
	43: {',', ';'},

	51: {'ö', 'Ö'},
	52: {'ç', 'Ç'},
	53: {'.', ':'},
	86: {'<', '>'},
}

// trFOverrides are the keys where Turkish F differs from US. F rearranges the
// letters themselves, so this table is much larger than the Q one.
var trFOverrides = map[int][2]rune{
	41: {'+', '*'},
	2:  {'1', '!'},
	3:  {'2', '"'},
	4:  {'3', '^'},
	5:  {'4', '$'},
	6:  {'5', '%'},
	7:  {'6', '&'},
	8:  {'7', '\''},
	9:  {'8', '('},
	10: {'9', ')'},
	11: {'0', '='},
	12: {'/', '?'},
	13: {'-', '_'},

	16: {'f', 'F'},
	17: {'g', 'G'},
	18: {'ğ', 'Ğ'},
	19: {'ı', 'I'},
	20: {'o', 'O'},
	21: {'d', 'D'},
	22: {'r', 'R'},
	23: {'n', 'N'},
	24: {'h', 'H'},
	25: {'p', 'P'},
	26: {'q', 'Q'},
	27: {'w', 'W'},

	30: {'u', 'U'},
	31: {'i', 'İ'},
	32: {'e', 'E'},
	33: {'a', 'A'},
	34: {'ü', 'Ü'},
	35: {'t', 'T'},
	36: {'k', 'K'},
	37: {'m', 'M'},
	38: {'l', 'L'},
	39: {'y', 'Y'},
	40: {'ş', 'Ş'},
	43: {'x', 'X'},

	44: {'j', 'J'},
	45: {'ö', 'Ö'},
	46: {'v', 'V'},
	47: {'c', 'C'},
	48: {'ç', 'Ç'},
	49: {'z', 'Z'},
	50: {'s', 'S'},
	51: {'b', 'B'},
	52: {'.', ':'},
	53: {',', ';'},
	86: {'<', '>'},
}

// layouts resolves a Layout to its override table. US has none.
var layouts = map[Layout]map[int][2]rune{
	LayoutUS:  nil,
	LayoutTRQ: trQOverrides,
	LayoutTRF: trFOverrides,
}

// KeycodeToRune returns the printable rune for a keycode in the active layout,
// considering shift. Returns 0 if the keycode has no printable character.
func KeycodeToRune(keycode int, shifted bool) rune {
	return KeycodeToRuneIn(CurrentLayout(), keycode, shifted)
}

// KeycodeToRuneIn is KeycodeToRune against a named layout, so a layout can be
// tested without disturbing the one the kiosk is running on.
func KeycodeToRuneIn(layout Layout, keycode int, shifted bool) rune {
	pair, ok := layouts[layout][keycode]
	if !ok {
		pair, ok = usKeycodeToRune[keycode]
	}
	if !ok {
		return 0
	}
	if shifted {
		return pair[1]
	}
	return pair[0]
}
