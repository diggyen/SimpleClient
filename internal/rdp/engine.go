package rdp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/diggyen/SimpleClient/internal/audio"
)

// Session is a live connection, whichever client runs it: the engine, which
// draws the remote desktop itself, or the built-in client (Client), whose
// frames the kiosk draws.
type Session interface {
	SendKey(linuxKeycode int, down bool) error
	SendMouse(x, y int, flags uint16) error
	SendMouseDown(button, x, y int)
	SendMouseUp(button, x, y int)
	SendWheel(notches, x, y int)
	Close() error
	Done() <-chan struct{}
	Failed() bool
}

// EnginePath is the session engine, sc-rdp: FreeRDP drawing straight to the
// display through KMS, with H.264 at the display's own refresh rate (see
// engine/ in the source). Where it is missing, or finds no display it can
// drive, sessions run on the built-in client.
var EnginePath = "/usr/bin/sc-rdp"

// hwdecPath is the engine's answer to whether the GPU decodes the sessions'
// H.264 (engine/hwdec.c): "cpu", or "gpu <render node> <kernel driver>
// <VA-API driver>".
var hwdecPath = "/run/sc-hwdec"

// ProbeDecoder has the engine find out, ahead of the first session, whether
// the GPU decodes H.264: sessions then start without the trial, and the kiosk
// can show its answer (Decoder). It returns once the trial is over.
func ProbeDecoder() {
	if _, err := os.Stat(EnginePath); err != nil {
		return
	}
	cmd := exec.Command(EnginePath, "--hwdec")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HOME=/tmp")
	_ = cmd.Run()
}

var decoder struct {
	sync.Mutex
	known bool
	gpu   string
}

// Decoder says what decodes the sessions' H.264: the GPU's maker ("Intel",
// "AMD"), or "" for the CPU. known is false until the engine has found out.
func Decoder() (gpu string, known bool) {
	decoder.Lock()
	defer decoder.Unlock()
	if !decoder.known {
		b, err := os.ReadFile(hwdecPath)
		if err != nil {
			return "", false
		}
		decoder.known = true
		if f := strings.Fields(string(b)); len(f) >= 3 && f[0] == "gpu" {
			switch f[2] {
			case "i915", "xe":
				decoder.gpu = "Intel"
			case "amdgpu", "radeon":
				decoder.gpu = "AMD"
			default:
				decoder.gpu = f[2]
			}
		}
	}
	return decoder.gpu, true
}

// ErrNoEngine reports that the engine cannot run this session, and the
// built-in client should.
var ErrNoEngine = errors.New("session engine unavailable")

// EngineError is a connection the engine could not make.
type EngineError struct {
	Class string // auth, unreachable, refused, tls or other
	Name  string // FreeRDP's name for the error
	Text  string
}

func (e *EngineError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Class, e.Text, e.Name)
}

// alsaConf is alsa-lib's system configuration. The engine plays and records
// through ALSA's "default" device, which is pointed here at the endpoints the
// kiosk chose: FreeRDP takes its options comma-separated, and a device name
// like plughw:0,3 cannot be written in them.
var alsaConf = "/etc/asound.conf"

// writeWait bounds a write to the engine. Input for an engine that has
// stopped reading is dropped rather than left to block the input loop, which
// has to stay free for Ctrl+Alt+End.
const writeWait = 200 * time.Millisecond

// Engine is a session run by sc-rdp.
type Engine struct {
	cmd    *exec.Cmd
	in     *os.File // the engine's fd 3
	mu     sync.Mutex
	done   chan struct{}
	failed atomic.Bool
	closed atomic.Bool

	// The display mode the engine drives: the pointer has to span it, and
	// it need not be the console's.
	Width, Height, Refresh int
	Display                string
}

// StartEngine connects to addr ("host:port") through the engine, with the
// engine options devices gives for the kiosk's USB devices. onPointer, if not
// nil, is called when the remote desktop moves the pointer. A nil
// session and ErrNoEngine mean the built-in client should be used instead.
func StartEngine(addr string, creds Credentials, devices []string, onPointer func(x, y int)) (*Engine, error) {
	if _, err := os.Stat(EnginePath); err != nil {
		return nil, ErrNoEngine
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, ErrNoEngine
	}
	stR, stW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, ErrNoEngine
	}

	cmd := exec.Command(EnginePath)
	cmd.ExtraFiles = []*os.File{inR, stW} // fd 3, fd 4
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HOME=/tmp")
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{inR, inW, stR, stW} {
			f.Close()
		}
		return nil, ErrNoEngine
	}
	inR.Close()
	stW.Close()

	e := &Engine{cmd: cmd, in: inW, done: make(chan struct{})}

	// The connection first, then "start". The password goes down the pipe:
	// on the command line every process could read it.
	var b strings.Builder
	for _, a := range engineArgs(addr, creds, devices) {
		fmt.Fprintf(&b, "arg %s\n", a)
	}
	fmt.Fprintf(&b, "password %s\nstart\n", creds.Password)
	e.write(b.String())

	up := make(chan error, 1)
	go e.watch(stR, up, onPointer)

	select {
	case err := <-up:
		if err != nil {
			e.Close()
			<-e.done
			if errors.Is(err, ErrNoEngine) {
				return nil, err
			}
			return nil, fmt.Errorf("RDP connect %s: %w", addr, err)
		}
		return e, nil
	case <-time.After(connectTimeout + 15*time.Second):
		e.Close()
		return nil, fmt.Errorf("RDP connect %s: timeout after %s", addr, connectTimeout+15*time.Second)
	}
}

// engineArgs are the FreeRDP arguments for one connection. The engine adds
// its own for the display: the resolution, the graphics pipeline, H.264.
func engineArgs(addr string, creds Credentials, devices []string) []string {
	args := []string{"/v:" + addr}
	if creds.Username != "" {
		args = append(args, "/u:"+creds.Username)
	}
	if creds.Domain != "" {
		args = append(args, "/d:"+creds.Domain)
	}
	args = append(args, fmt.Sprintf("/kbd:layout:0x%08X", KeyboardLayout))

	if Audio != nil {
		out, haveOut := Audio.Sel.Output()
		in, haveIn := Audio.Sel.Input()
		if (haveOut || haveIn) && writeALSAConf(out, haveOut, in, haveIn) == nil {
			if haveOut {
				audio.PrepareMixer(out.Card)
				args = append(args, "/sound:sys:alsa")
			}
			if haveIn {
				audio.PrepareMixer(in.Card)
				args = append(args, "/microphone:sys:alsa")
			}
		}
	}
	args = append(args, devices...)

	if Debug {
		args = append(args, "/log-level:DEBUG")
	} else {
		args = append(args, "/log-level:WARN")
	}
	return args
}

// writeALSAConf points ALSA's default playback and capture devices at the
// chosen endpoints, through plug so any rate and format the server sends is
// converted to what the hardware takes.
func writeALSAConf(out audio.Device, haveOut bool, in audio.Device, haveIn bool) error {
	var b strings.Builder
	b.WriteString("pcm.!default {\n\ttype asym\n")
	if haveOut {
		fmt.Fprintf(&b, "\tplayback.pcm { type plug slave.pcm \"hw:%d,%d\" }\n", out.Card, out.Dev)
	}
	if haveIn {
		fmt.Fprintf(&b, "\tcapture.pcm { type plug slave.pcm \"hw:%d,%d\" }\n", in.Card, in.Dev)
	}
	b.WriteString("}\n")
	card := in.Card
	if haveOut {
		card = out.Card
	}
	fmt.Fprintf(&b, "ctl.!default { type hw card %d }\n", card)
	return os.WriteFile(alsaConf, []byte(b.String()), 0o644)
}

// watch reads the engine's status until it exits, reporting on up whether the
// session came up.
func (e *Engine) watch(r io.ReadCloser, up chan<- error, onPointer func(x, y int)) {
	reported := false
	report := func(err error) {
		if !reported {
			reported = true
			up <- err
		}
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "MODE": // MODE w h hz display
			if len(f) >= 4 {
				e.Width, _ = strconv.Atoi(f[1])
				e.Height, _ = strconv.Atoi(f[2])
				e.Refresh, _ = strconv.Atoi(f[3])
			}
			if len(f) >= 5 {
				e.Display = f[4]
			}
		case "CONNECTED":
			report(nil)
		case "POINTER":
			if len(f) >= 3 && onPointer != nil {
				x, _ := strconv.Atoi(f[1])
				y, _ := strconv.Atoi(f[2])
				onPointer(x, y)
			}
		case "ERROR": // ERROR class name text...
			if len(f) >= 2 && f[1] == "display" {
				report(ErrNoEngine)
				break
			}
			ee := &EngineError{Class: "other"}
			if len(f) >= 2 {
				ee.Class = f[1]
			}
			if len(f) >= 3 {
				ee.Name = f[2]
			}
			if len(f) >= 4 {
				ee.Text = strings.Join(f[3:], " ")
			}
			report(ee)
		case "END": // END logoff|lost|quit
			e.failed.Store(len(f) >= 2 && f[1] == "lost")
		}
	}
	r.Close()

	// The status pipe closes when the engine exits. An engine that died
	// before saying anything is one that cannot run here — a library missing,
	// a crash on this hardware — and the built-in client takes over.
	err := e.cmd.Wait()
	report(fmt.Errorf("%w: %v", ErrNoEngine, err))
	e.closed.Store(true)
	e.in.Close()
	close(e.done)
}

// write sends lines to the engine, giving up on one it has stopped reading.
func (e *Engine) write(s string) {
	if e.closed.Load() {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.in.SetWriteDeadline(time.Now().Add(writeWait))
	_, _ = io.WriteString(e.in, s)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SendKey forwards a key press or release.
func (e *Engine) SendKey(linuxKeycode int, down bool) error {
	sc := linuxToRDPScanCode(linuxKeycode)
	if sc == 0 {
		return nil
	}
	e.write(fmt.Sprintf("k %d %d %d\n", sc, b2i(isExtendedKey(linuxKeycode)), b2i(down)))
	return nil
}

// SendMouse moves the pointer. The engine moves it on the screen at once and
// sends the server the last of a burst of moves.
func (e *Engine) SendMouse(x, y int, _ uint16) error {
	e.write(fmt.Sprintf("m %d %d\n", x, y))
	return nil
}

// SendMouseDown sends a button press: 1 left, 2 right, 3 middle, 8 back, 9
// forward.
func (e *Engine) SendMouseDown(button, x, y int) {
	e.write(fmt.Sprintf("b %d 1 %d %d\n", button, x, y))
}

// SendMouseUp sends a button release.
func (e *Engine) SendMouseUp(button, x, y int) {
	e.write(fmt.Sprintf("b %d 0 %d %d\n", button, x, y))
}

// SendWheel sends wheel notches; positive scrolls away from the user.
func (e *Engine) SendWheel(notches, x, y int) {
	e.write(fmt.Sprintf("w %d %d %d\n", notches, x, y))
}

// Close ends the session. It does not wait: the engine is asked to quit and,
// if it has not within a few seconds, killed.
func (e *Engine) Close() error {
	e.write("q\n")
	go func() {
		select {
		case <-e.done:
		case <-time.After(3 * time.Second):
			_ = e.cmd.Process.Kill()
		}
	}()
	return nil
}

// Done is closed when the engine has exited.
func (e *Engine) Done() <-chan struct{} { return e.done }

// Failed reports whether the session ended because the connection died, as
// opposed to a logoff or a disconnect from either side.
func (e *Engine) Failed() bool { return e.failed.Load() }
