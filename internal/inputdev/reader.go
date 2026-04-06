package inputdev

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
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
	Sec     int64
	Usec    int64
	Type    uint16
	Code    uint16
	Value   int32
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

	// /proc/bus/input/devices lines look like:
	// N: Name="AT Translated Set 2 keyboard"
	// H: Handlers=sysrq kbd event0
	var name string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "N: Name=") {
			name = strings.ToLower(line)
		}
		if strings.HasPrefix(line, "H: Handlers=") {
			if matchesKind(name, kind) {
				// Extract event node.
				fields := strings.Fields(line)
				for _, f := range fields {
					if strings.HasPrefix(f, "event") {
						return "/dev/input/" + f, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("no %s device found in /proc/bus/input/devices", kind)
}

func matchesKind(nameLine, kind string) bool {
	switch kind {
	case "keyboard":
		return strings.Contains(nameLine, "keyboard")
	case "mouse":
		return strings.Contains(nameLine, "mouse") || strings.Contains(nameLine, "touchpad")
	}
	return false
}
