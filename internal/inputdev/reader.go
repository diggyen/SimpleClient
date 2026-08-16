package inputdev

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"

	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// EventType classifies an InputEvent.
type EventType int

const (
	EvKey         EventType = iota // keyboard key press/release or mouse button
	EvMouseMove                    // relative mouse movement
	EvMouseButton                  // mouse button press/release
)

// InputEvent carries a single user input action.
type InputEvent struct {
	Type    EventType
	KeyCode int  // Linux keycode (EvKey)
	Rune    rune // Printable character, 0 for special keys
	DX, DY  int  // Relative mouse movement
	MouseX  int  // Absolute mouse X (clamped)
	MouseY  int  // Absolute mouse Y (clamped)
	Button  int  // 1=left, 2=right, 3=middle (EvMouseButton)
	Pressed bool // true on press, false on release
}

// linuxInputEvent mirrors the 24-byte Linux struct input_event.
type linuxInputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

const (
	evTypeSyn = 0x00
	evTypeKey = 0x01
	evTypeRel = 0x02

	relX = 0x00
	relY = 0x01

	// EV_KEY values
	keyRelease = 0
	keyPress   = 1
	keyRepeat  = 2
)

// Reader reads keyboard and mouse events from evdev devices.
type Reader struct {
	kbd     *os.File
	mouse   *os.File
	events  chan InputEvent
	screenW int
	screenH int
	mouseX  int64 // atomic
	mouseY  int64 // atomic
	shiftOn int32 // atomic bool (1=shifted)
	ctrlOn  int32 // atomic bool
	altOn   int32 // atomic bool
	wg      sync.WaitGroup
	quit    chan struct{}
}

// New opens the keyboard and mouse evdev devices and starts reading goroutines.
func New(kbdPath, mousePath string, screenW, screenH int) (*Reader, error) {
	r := &Reader{
		events:  make(chan InputEvent, 64),
		screenW: screenW,
		screenH: screenH,
		quit:    make(chan struct{}),
	}
	// Start mouse at center.
	atomic.StoreInt64(&r.mouseX, int64(screenW/2))
	atomic.StoreInt64(&r.mouseY, int64(screenH/2))

	var err error
	if kbdPath != "" {
		r.kbd, err = os.Open(kbdPath)
		if err != nil {
			return nil, fmt.Errorf("open keyboard %s: %w", kbdPath, err)
		}
		r.wg.Add(1)
		go r.readKeyboard()
	}
	if mousePath != "" {
		r.mouse, err = os.Open(mousePath)
		if err != nil {
			if r.kbd != nil {
				r.kbd.Close()
			}
			return nil, fmt.Errorf("open mouse %s: %w", mousePath, err)
		}
		r.wg.Add(1)
		go r.readMouse()
	}
	return r, nil
}

// Events returns the channel of input events.
func (r *Reader) Events() <-chan InputEvent { return r.events }

// MousePos returns the current mouse position (thread-safe).
func (r *Reader) MousePos() (int, int) {
	return int(atomic.LoadInt64(&r.mouseX)), int(atomic.LoadInt64(&r.mouseY))
}

// CtrlDown reports whether a Ctrl key is currently held (thread-safe).
func (r *Reader) CtrlDown() bool { return atomic.LoadInt32(&r.ctrlOn) == 1 }

// AltDown reports whether an Alt key is currently held (thread-safe).
func (r *Reader) AltDown() bool { return atomic.LoadInt32(&r.altOn) == 1 }

// Close stops reading and closes device files.
func (r *Reader) Close() {
	close(r.quit)
	if r.kbd != nil {
		r.kbd.Close()
	}
	if r.mouse != nil {
		r.mouse.Close()
	}
	r.wg.Wait()
}

func (r *Reader) readKeyboard() {
	defer r.wg.Done()
	for {
		var ev linuxInputEvent
		if err := binary.Read(r.kbd, binary.LittleEndian, &ev); err != nil {
			if err == io.EOF {
				return
			}
			select {
			case <-r.quit:
				return
			default:
			}
			return
		}

		if ev.Type != evTypeKey {
			continue
		}

		code := int(ev.Code)
		pressed := ev.Value == keyPress || ev.Value == keyRepeat

		// Track modifier keys.
		switch code {
		case KeyLeftShift, KeyRightShift:
			if pressed {
				atomic.StoreInt32(&r.shiftOn, 1)
			} else {
				atomic.StoreInt32(&r.shiftOn, 0)
			}
			continue
		case KeyCtrl:
			if pressed {
				atomic.StoreInt32(&r.ctrlOn, 1)
			} else {
				atomic.StoreInt32(&r.ctrlOn, 0)
			}
			continue
		case KeyAlt:
			if pressed {
				atomic.StoreInt32(&r.altOn, 1)
			} else {
				atomic.StoreInt32(&r.altOn, 0)
			}
			continue
		}

		if !pressed {
			continue
		}

		shifted := atomic.LoadInt32(&r.shiftOn) == 1
		ch := KeycodeToRune(code, shifted)

		select {
		case r.events <- InputEvent{
			Type:    EvKey,
			KeyCode: code,
			Rune:    ch,
			Pressed: true,
		}:
		case <-r.quit:
			return
		}
	}
}

func (r *Reader) readMouse() {
	defer r.wg.Done()
	var ev linuxInputEvent
	for {
		if err := binary.Read(r.mouse, binary.LittleEndian, &ev); err != nil {
			select {
			case <-r.quit:
				return
			default:
			}
			return
		}

		switch ev.Type {
		case evTypeRel:
			switch ev.Code {
			case relX:
				x := clampMouse(int(atomic.LoadInt64(&r.mouseX))+int(ev.Value), r.screenW)
				atomic.StoreInt64(&r.mouseX, int64(x))
			case relY:
				y := clampMouse(int(atomic.LoadInt64(&r.mouseY))+int(ev.Value), r.screenH)
				atomic.StoreInt64(&r.mouseY, int64(y))
			}
			mx, my := r.MousePos()
			select {
			case r.events <- InputEvent{
				Type:   EvMouseMove,
				MouseX: mx,
				MouseY: my,
			}:
			default: // drop stale mouse moves
			}

		case evTypeKey:
			mx, my := r.MousePos()
			btn := 0
			switch ev.Code {
			case BtnLeft:
				btn = 1
			case BtnRight:
				btn = 2
			case BtnMiddle:
				btn = 3
			}
			if btn == 0 {
				continue
			}
			pressed := ev.Value == keyPress
			select {
			case r.events <- InputEvent{
				Type:    EvMouseButton,
				Button:  btn,
				Pressed: pressed,
				MouseX:  mx,
				MouseY:  my,
			}:
			case <-r.quit:
				return
			}
		}
	}
}

func clampMouse(v, max int) int {
	if v < 0 {
		return 0
	}
	if v >= max {
		return max - 1
	}
	return v
}

// DetectKeyboard scans /proc/bus/input/devices for a keyboard device.
func DetectKeyboard() (string, error) {
	return detectDevice("keyboard")
}

// DetectMouse scans /proc/bus/input/devices for a mouse device.
func DetectMouse() (string, error) {
	return detectDevice("mouse")
}

func detectDevice(kind string) (string, error) {
	f, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return "", fmt.Errorf("open /proc/bus/input/devices: %w", err)
	}
	defer f.Close()

	return findDevice(f, kind)
}

// Capability bits from the kernel's input headers. Detection keys off these
// rather than off the device name: the name is the USB product string, so a
// real keyboard is as likely to announce itself as "Logitech USB Receiver" or
// "HID 046a:0011" as it is to contain the word "keyboard". Matching the name
// worked in QEMU, whose emulated keyboard is called "AT Translated Set 2
// keyboard", and failed on the hardware this is built for.
const (
	evKeyBit = 0x01 // EV_KEY
	evRelBit = 0x02 // EV_REL
	evAbsBit = 0x03 // EV_ABS

	keyEnter = 28
	keyA     = 30
	keyZ     = 44
	keySpace = 57

	btnLeft  = 0x110
	btnTouch = 0x14a
)

// bitmap is a capability bitmap from a "B: " line, indexed from bit 0 up.
type bitmap []uint64

// parseBitmap reads the kernel's bitmap format: groups of hex longs, separated
// by spaces, highest-order group printed first.
//
// The group width is the kernel's BITS_PER_LONG. This binary is built for
// linux/amd64 only, so that is 64; on a 32-bit kernel every bit above 31 would
// land in the wrong group.
func parseBitmap(v string) bitmap {
	fields := strings.Fields(v)
	b := make(bitmap, len(fields))
	for i, f := range fields {
		n, err := strconv.ParseUint(f, 16, 64)
		if err != nil {
			continue
		}
		b[len(fields)-1-i] = n
	}
	return b
}

func (b bitmap) has(bit int) bool {
	i := bit / 64
	if i < 0 || i >= len(b) {
		return false
	}
	return b[i]&(1<<uint(bit%64)) != 0
}

// inputBlock is one device stanza from /proc/bus/input/devices.
type inputBlock struct {
	name     string
	handlers []string
	ev       bitmap
	key      bitmap
	rel      bitmap
}

// isKeyboard reports whether the device can type.
//
// The test is for ordinary letter keys, which is what separates a keyboard from
// the other things the kernel gives a "kbd" handler to. A power button, a lid
// switch and an ACPI video bus are all EV_KEY devices carrying a handful of
// codes, and on real hardware they enumerate ahead of the keyboard — so keying
// off the handler list, or off EV_KEY alone, picks the power button.
func (d inputBlock) isKeyboard() bool {
	if !d.ev.has(evKeyBit) {
		return false
	}
	for _, k := range []int{keyA, keyZ, keyEnter, keySpace} {
		if !d.key.has(k) {
			return false
		}
	}
	return true
}

// isPointer reports whether the device can move a cursor: a relative device
// with both axes and a left button, or a touchpad.
func (d inputBlock) isPointer() bool {
	if d.ev.has(evRelBit) && d.rel.has(relX) && d.rel.has(relY) && d.key.has(btnLeft) {
		return true
	}
	return d.ev.has(evAbsBit) && d.key.has(btnTouch)
}

// node returns the device's evdev path, or "" if it has no event handler.
//
// The handler list has to be split after stripping "Handlers=", not before:
// when evdev is the only handler the line reads "H: Handlers=event1" and the
// node is glued to the key.
func (d inputBlock) node() string {
	for _, h := range d.handlers {
		if strings.HasPrefix(h, "event") {
			return "/dev/input/" + h
		}
	}
	return ""
}

// findDevice scans the /proc/bus/input/devices format for the first device with
// the capabilities kind implies, and returns its evdev node.
//
// A device block looks like:
//
//	I: Bus=0011 Vendor=0001 Product=0001 Version=ab41
//	N: Name="AT Translated Set 2 keyboard"
//	H: Handlers=sysrq kbd event0
//	B: EV=120013
//	B: KEY=402000000 3803078f800d001 feffffdfffefffff fffffffffffffffe
func findDevice(r io.Reader, kind string) (string, error) {
	var (
		cur   inputBlock
		found string
	)

	match := func(d inputBlock) bool {
		switch kind {
		case "keyboard":
			return d.isKeyboard()
		case "mouse":
			return d.isPointer()
		}
		return false
	}

	flush := func() {
		if found != "" {
			return
		}
		if match(cur) {
			found = cur.node()
		}
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "I: "):
			flush()
			cur = inputBlock{}
		case strings.HasPrefix(line, "N: Name="):
			cur.name = strings.Trim(strings.TrimPrefix(line, "N: Name="), `"`)
		case strings.HasPrefix(line, "H: Handlers="):
			cur.handlers = strings.Fields(strings.TrimPrefix(line, "H: Handlers="))
		case strings.HasPrefix(line, "B: EV="):
			cur.ev = parseBitmap(strings.TrimPrefix(line, "B: EV="))
		case strings.HasPrefix(line, "B: KEY="):
			cur.key = parseBitmap(strings.TrimPrefix(line, "B: KEY="))
		case strings.HasPrefix(line, "B: REL="):
			cur.rel = parseBitmap(strings.TrimPrefix(line, "B: REL="))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading input devices: %w", err)
	}
	flush() // the last block has no "I: " after it

	if found == "" {
		return "", fmt.Errorf("no %s device found in /proc/bus/input/devices", kind)
	}
	return found, nil
}
