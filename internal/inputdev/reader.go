package inputdev

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"

	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EventType classifies an InputEvent.
type EventType int

const (
	EvKey         EventType = iota // keyboard key press/release or mouse button
	EvMouseMove                    // relative mouse movement
	EvMouseButton                  // mouse button press/release
	EvMouseWheel                   // wheel notch (Wheel > 0 away from the user)
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
	Wheel   int  // notches (EvMouseWheel), positive = away from the user
}

// storeBool sets an atomic flag used as a bool.
func storeBool(p *int32, v bool) {
	var n int32
	if v {
		n = 1
	}
	atomic.StoreInt32(p, n)
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
	evTypeAbs = 0x03

	absX = 0x00
	absY = 0x01

	// BTN_MOUSE..BTN_TASK: the buttons a mouse or touchpad reports.
	btnMouseFirst = 0x110
	btnMouseLast  = 0x117

	// INPUT_PROP_POINTER: an indirect absolute device, i.e. a touchpad.
	inputPropPointer = 0x00

	relX     = 0x00
	relY     = 0x01
	relWheel = 0x08

	// EV_KEY values
	keyRelease = 0
	keyPress   = 1
	keyRepeat  = 2
)

// Reader reads keyboard and pointer events from evdev devices.
//
// Every device that can type or move a pointer is read, not just the first of
// each: a wireless receiver commonly exposes its keys on one node and its
// media keys or mouse on another, a laptop has a touchpad and a trackpoint, and
// a kiosk may have two keyboards plugged in. Devices are also picked up after
// boot — a keyboard plugged in once the kiosk is already running works — and
// dropped again when they are unplugged.
type Reader struct {
	events  chan InputEvent
	screenW int
	screenH int
	mouseX  int64 // atomic
	mouseY  int64 // atomic
	shiftOn int32 // atomic bool (1=shifted)
	ctrlOn  int32 // atomic bool
	altOn   int32 // atomic bool
	capsOn  int32 // atomic bool, toggled by Caps Lock presses
	wg      sync.WaitGroup
	quit    chan struct{}

	mu    sync.Mutex
	open  map[string]*os.File // evdev node → open file
	fixed bool                // explicit paths given: no autodetect/hotplug
}

// hotplugInterval is how often the device list is re-read for keyboards and
// pointers plugged in after boot. /proc/bus/input/devices is a cheap read.
const hotplugInterval = 1500 * time.Millisecond

// New opens the given keyboard and mouse evdev devices and starts reading
// them. Either may be empty. Explicit paths switch auto-detection off: they are
// the override for the one machine where it picks the wrong node.
func New(kbdPath, mousePath string, screenW, screenH int) (*Reader, error) {
	r := newReader(screenW, screenH)
	r.fixed = true
	for _, p := range []string{kbdPath, mousePath} {
		if p == "" {
			continue
		}
		if err := r.openDevice(p, deviceInfo{}); err != nil {
			r.Close()
			return nil, err
		}
	}
	return r, nil
}

// NewAuto opens every keyboard and pointer present and keeps watching for new
// ones. It never fails: a kiosk with nothing plugged in yet still boots, and
// starts responding the moment a keyboard appears.
func NewAuto(screenW, screenH int) *Reader {
	r := newReader(screenW, screenH)
	r.rescan()
	r.wg.Add(1)
	go r.hotplug()
	return r
}

func newReader(screenW, screenH int) *Reader {
	r := &Reader{
		events:  make(chan InputEvent, 256),
		screenW: screenW,
		screenH: screenH,
		quit:    make(chan struct{}),
		open:    map[string]*os.File{},
	}
	// Start mouse at center.
	atomic.StoreInt64(&r.mouseX, int64(screenW/2))
	atomic.StoreInt64(&r.mouseY, int64(screenH/2))
	return r
}

// Devices lists the evdev nodes currently being read, for the boot log.
func (r *Reader) Devices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.open))
	for p := range r.open {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (r *Reader) hotplug() {
	defer r.wg.Done()
	t := time.NewTicker(hotplugInterval)
	defer t.Stop()
	for {
		select {
		case <-r.quit:
			return
		case <-t.C:
			r.rescan()
		}
	}
}

// rescan opens every keyboard or pointer that is not open yet.
func (r *Reader) rescan() {
	f, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return
	}
	blocks, _ := parseDevices(f)
	f.Close()
	for _, b := range blocks {
		if !b.isKeyboard() && !b.isPointer() {
			continue
		}
		node := b.node()
		if node == "" {
			continue
		}
		r.mu.Lock()
		_, already := r.open[node]
		r.mu.Unlock()
		if already {
			continue
		}
		_ = r.openDevice(node, deviceInfoFor(b))
	}
}

// openDevice opens one evdev node and starts its reader goroutine.
func (r *Reader) openDevice(path string, info deviceInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open input device %s: %w", path, err)
	}
	if info.abs {
		info.absX, info.absY = readAbsRange(f, absX), readAbsRange(f, absY)
	}
	r.mu.Lock()
	select {
	case <-r.quit:
		r.mu.Unlock()
		f.Close()
		return nil
	default:
	}
	r.open[path] = f
	r.wg.Add(1)
	r.mu.Unlock()
	go r.readDevice(path, f, info)
	return nil
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

// CapsLockOn reports whether Caps Lock is engaged (thread-safe). It is tracked
// from key presses, starting off, which is the state every kiosk boots in.
func (r *Reader) CapsLockOn() bool { return atomic.LoadInt32(&r.capsOn) == 1 }

// Close stops reading and closes device files.
func (r *Reader) Close() {
	r.mu.Lock()
	select {
	case <-r.quit:
	default:
		close(r.quit)
	}
	for _, f := range r.open {
		f.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// emit delivers an event, giving up only when the reader is closing.
func (r *Reader) emit(ev InputEvent) bool {
	select {
	case r.events <- ev:
		return true
	case <-r.quit:
		return false
	}
}

// deviceInfo says how a node's absolute axes are to be read.
type deviceInfo struct {
	abs        bool // reports ABS_X/ABS_Y
	touchpad   bool // ABS is a finger on a pad: move relatively, tap to click
	absX, absY absRange
}

func deviceInfoFor(b inputBlock) deviceInfo {
	abs := b.ev.has(evAbsBit) && b.abs.has(absX) && b.abs.has(absY)
	return deviceInfo{
		abs:      abs,
		touchpad: abs && b.prop.has(inputPropPointer),
	}
}

// absRange is an absolute axis's reported extent.
type absRange struct{ min, max int32 }

func (a absRange) span() int32 {
	if a.max > a.min {
		return a.max - a.min
	}
	return 0
}

// tapMaxDuration and tapMaxTravel define a touchpad tap: a touch this short
// that moved the pointer less than this far is a left click. Most touchpads in
// the hardware this runs on have no physical buttons worth the name.
const (
	tapMaxDuration = 180 * time.Millisecond
	tapMaxTravel   = 8
)

// readDevice reads one evdev node until it is closed or unplugged. Keys,
// buttons, relative and absolute motion are all handled here, so a combined
// device — a keyboard with a built-in touchpad on one node — works too.
func (r *Reader) readDevice(path string, f *os.File, info deviceInfo) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		if r.open[path] == f {
			delete(r.open, path)
		}
		r.mu.Unlock()
		f.Close()
	}()

	var (
		ev    linuxInputEvent
		moved bool
		// Touchpad state: the last finger position and the current tap.
		touching         bool
		lastAX, lastAY   int32
		haveAX, haveAY   bool
		curAX, curAY     int32
		tapStart         time.Time
		tapTravel        int
		absDirty         bool
		accumDX, accumDY float64
	)

	for {
		if err := binary.Read(f, binary.LittleEndian, &ev); err != nil {
			// EOF on a test file, ENODEV on an unplugged device, EBADF on
			// Close: all mean this node is done.
			return
		}

		switch ev.Type {
		case evTypeKey:
			code := int(ev.Code)
			switch {
			case code == btnTouch && info.touchpad:
				if ev.Value == keyPress {
					touching, haveAX, haveAY = true, false, false
					tapStart, tapTravel = time.Now(), 0
				} else if ev.Value == keyRelease {
					touching = false
					if time.Since(tapStart) < tapMaxDuration && tapTravel < tapMaxTravel {
						mx, my := r.MousePos()
						for _, down := range []bool{true, false} {
							if !r.emit(InputEvent{Type: EvMouseButton, Button: 1, Pressed: down, MouseX: mx, MouseY: my}) {
								return
							}
						}
					}
				}
			case code >= btnMouseFirst && code <= btnMouseLast:
				if !r.handleButton(code, ev.Value) {
					return
				}
			case code < 0x100 || code >= 0x160:
				if !r.handleKey(code, ev.Value) {
					return
				}
			}

		case evTypeRel:
			switch ev.Code {
			case relX:
				r.moveBy(int(ev.Value), 0)
				moved = true
			case relY:
				r.moveBy(0, int(ev.Value))
				moved = true
			case relWheel:
				if ev.Value == 0 {
					continue
				}
				mx, my := r.MousePos()
				if !r.emit(InputEvent{Type: EvMouseWheel, Wheel: int(ev.Value), MouseX: mx, MouseY: my}) {
					return
				}
			}

		case evTypeAbs:
			if !info.abs {
				continue
			}
			switch ev.Code {
			case absX:
				curAX, absDirty = ev.Value, true
			case absY:
				curAY, absDirty = ev.Value, true
			}

		case evTypeSyn:
			if absDirty {
				absDirty = false
				if info.touchpad {
					// A finger moves the pointer by how far it travelled, like
					// a mouse; where on the pad it landed does not matter.
					if touching && haveAX && haveAY {
						dx, dy := r.padDelta(info, curAX-lastAX, curAY-lastAY, &accumDX, &accumDY)
						if dx != 0 || dy != 0 {
							r.moveBy(dx, dy)
							tapTravel += abs(dx) + abs(dy)
							moved = true
						}
					}
					lastAX, lastAY, haveAX, haveAY = curAX, curAY, true, true
				} else {
					// A tablet or touchscreen — and QEMU's USB tablet, the
					// default pointer in most VMs — reports where it is.
					r.moveTo(info, curAX, curAY)
					moved = true
				}
			}
			// Motion is reported once per evdev packet, on its SYN_REPORT,
			// not once per axis. A diagonal movement arrives as REL_X + REL_Y
			// + SYN; emitting on each axis doubled the pointer traffic to the
			// server and drew the pointer at a position that never existed.
			if moved {
				moved = false
				mx, my := r.MousePos()
				select {
				case r.events <- InputEvent{Type: EvMouseMove, MouseX: mx, MouseY: my}:
				default: // drop stale mouse moves
				}
			}
		}
	}
}

// padDelta scales a touchpad's travel to screen pixels: crossing the pad's
// width moves the pointer about one and a half screen widths. Fractions are
// carried over so slow, precise movements are not lost to rounding.
func (r *Reader) padDelta(info deviceInfo, dx, dy int32, ax, ay *float64) (int, int) {
	sx, sy := info.absX.span(), info.absY.span()
	if sx == 0 || sy == 0 {
		return int(dx), int(dy)
	}
	f := 1.5 * float64(r.screenW) / float64(sx)
	*ax += float64(dx) * f
	*ay += float64(dy) * f
	ix, iy := int(*ax), int(*ay)
	*ax -= float64(ix)
	*ay -= float64(iy)
	return ix, iy
}

func (r *Reader) moveBy(dx, dy int) {
	if dx != 0 {
		x := clampMouse(int(atomic.LoadInt64(&r.mouseX))+dx, r.screenW)
		atomic.StoreInt64(&r.mouseX, int64(x))
	}
	if dy != 0 {
		y := clampMouse(int(atomic.LoadInt64(&r.mouseY))+dy, r.screenH)
		atomic.StoreInt64(&r.mouseY, int64(y))
	}
}

func (r *Reader) moveTo(info deviceInfo, ax, ay int32) {
	sx, sy := info.absX.span(), info.absY.span()
	if sx == 0 || sy == 0 {
		return
	}
	x := int(int64(ax-info.absX.min) * int64(r.screenW-1) / int64(sx))
	y := int(int64(ay-info.absY.min) * int64(r.screenH-1) / int64(sy))
	atomic.StoreInt64(&r.mouseX, int64(clampMouse(x, r.screenW)))
	atomic.StoreInt64(&r.mouseY, int64(clampMouse(y, r.screenH)))
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// handleButton emits a mouse button press or release. Repeats are ignored.
func (r *Reader) handleButton(code int, value int32) bool {
	btn := 0
	switch code {
	case BtnLeft:
		btn = 1
	case BtnRight:
		btn = 2
	case BtnMiddle:
		btn = 3
	}
	if btn == 0 || value == keyRepeat {
		return true
	}
	mx, my := r.MousePos()
	return r.emit(InputEvent{
		Type:    EvMouseButton,
		Button:  btn,
		Pressed: value == keyPress,
		MouseX:  mx,
		MouseY:  my,
	})
}

// handleKey tracks modifiers and emits a key press or release.
func (r *Reader) handleKey(code int, value int32) bool {
	// Auto-repeat counts as another press: holding backspace in a dialog
	// should erase, and a remote session expects the repeats its keyboard is
	// generating.
	down := value == keyPress || value == keyRepeat

	// Track the modifiers — and then forward them like any other key.
	//
	// They used to be swallowed here, which was fine while the only consumer
	// was the local UI: it reads the tracked flags, not the events. But a live
	// RDP session forwards these events to the remote machine, and a remote
	// with no Shift, Ctrl or Alt cannot be typed on, never mind driven.
	switch code {
	case KeyLeftShift, KeyRightShift:
		storeBool(&r.shiftOn, down)
	case KeyCtrl, KeyRightCtrl:
		storeBool(&r.ctrlOn, down)
	case KeyAlt:
		storeBool(&r.altOn, down)
	case KeyCapsLock:
		// Toggled on the first press only; a held key auto-repeats.
		if value == keyPress {
			storeBool(&r.capsOn, atomic.LoadInt32(&r.capsOn) == 0)
		}
	}

	// Releases are emitted too. Sending only presses left every key held down
	// on the remote for the rest of the session — one keystroke and the far
	// end is stuck repeating it. Local screens ignore releases; see
	// handleInput.
	var ch rune
	if down {
		ch = KeycodeToRuneCaps(code, atomic.LoadInt32(&r.shiftOn) == 1, r.CapsLockOn())
	}
	return r.emit(InputEvent{
		Type:    EvKey,
		KeyCode: code,
		Rune:    ch,
		Pressed: down,
	})
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
	abs      bitmap
	prop     bitmap
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
	// A touchpad, a touchscreen, or a tablet — QEMU's USB tablet is the default
	// pointer in most virtual machines and reports BTN_LEFT, not BTN_TOUCH.
	return d.ev.has(evAbsBit) && d.abs.has(absX) && d.abs.has(absY) &&
		(d.key.has(btnTouch) || d.key.has(btnLeft))
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
	blocks, err := parseDevices(r)
	if err != nil {
		return "", err
	}
	for _, d := range blocks {
		var ok bool
		switch kind {
		case "keyboard":
			ok = d.isKeyboard()
		case "mouse":
			ok = d.isPointer()
		}
		if ok {
			if n := d.node(); n != "" {
				return n, nil
			}
		}
	}
	return "", fmt.Errorf("no %s device found in /proc/bus/input/devices", kind)
}

// parseDevices splits the /proc/bus/input/devices format into device blocks.
func parseDevices(r io.Reader) ([]inputBlock, error) {
	var (
		blocks []inputBlock
		cur    inputBlock
		inside bool
	)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "I: "):
			if inside {
				blocks = append(blocks, cur)
			}
			cur, inside = inputBlock{}, true
		case strings.HasPrefix(line, "N: Name="):
			cur.name = strings.Trim(strings.TrimPrefix(line, "N: Name="), `"`)
		case strings.HasPrefix(line, "H: Handlers="):
			cur.handlers = strings.Fields(strings.TrimPrefix(line, "H: Handlers="))
		case strings.HasPrefix(line, "B: PROP="):
			cur.prop = parseBitmap(strings.TrimPrefix(line, "B: PROP="))
		case strings.HasPrefix(line, "B: EV="):
			cur.ev = parseBitmap(strings.TrimPrefix(line, "B: EV="))
		case strings.HasPrefix(line, "B: KEY="):
			cur.key = parseBitmap(strings.TrimPrefix(line, "B: KEY="))
		case strings.HasPrefix(line, "B: REL="):
			cur.rel = parseBitmap(strings.TrimPrefix(line, "B: REL="))
		case strings.HasPrefix(line, "B: ABS="):
			cur.abs = parseBitmap(strings.TrimPrefix(line, "B: ABS="))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading input devices: %w", err)
	}
	if inside {
		blocks = append(blocks, cur) // the last block has no "I: " after it
	}
	return blocks, nil
}
