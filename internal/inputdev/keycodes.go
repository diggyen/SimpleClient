package inputdev

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

// linuxKeycodeToRune maps Linux keycode → [unshifted, shifted] runes.
// 0 rune means no printable character (special key).
var linuxKeycodeToRune = map[int][2]rune{
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

// KeycodeToRune returns the printable rune for a keycode, considering shift.
// Returns 0 if the keycode has no printable character.
func KeycodeToRune(keycode int, shifted bool) rune {
	pair, ok := linuxKeycodeToRune[keycode]
	if !ok {
		return 0
	}
	if shifted {
		return pair[1]
	}
	return pair[0]
}
