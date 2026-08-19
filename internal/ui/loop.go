package ui

import (
	"context"
	"image"
	"strings"
	"time"

	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/network"
	"github.com/diggyen/SimpleClient/internal/rdp"
)

// SessionState holds the state of an active RDP session.
type SessionState struct {
	Host      domain.Host
	Client    *rdp.Client
	Writer    *rdp.FrameWriter
	Connected bool
	Error     string
}

// Run is the main kiosk loop. It never returns.
//
// All UIState access goes through state.Mu: the connect goroutine started from
// the credential modal mutates the same struct concurrently with this loop.
// Nothing under the lock performs I/O that can block indefinitely — rdp.New and
// the RDP frame loop both run outside it — so holding it across a render is safe.
func Run(fb framebuffer.Device, input *inputdev.Reader, scan domain.Scanner, cfg config.Config) {
	state := &UIState{}
	backBuf := image.NewRGBA(fb.Bounds())

	// The reader starts the pointer at the centre of the screen; mirror that so
	// the first session starts with its cursor somewhere sensible rather than
	// in the top-left corner.
	state.MouseX, state.MouseY = input.MousePos()

	var session *SessionState

	// Start initial scan.
	ctx, cancelScan := context.WithCancel(context.Background())
	cidr, _ := network.DetectCIDR()
	scanCh := scan.Start(ctx, []string{cidr})

	ticker := time.NewTicker(16 * time.Millisecond) // ~60 FPS
	state.Dirty = true
	spinTick := 0

	maxRows := layoutDiscovery(fb.Bounds()).Rows

	for {
		select {
		// ── Scan events ──────────────────────────────────────────────────────
		case ev, ok := <-scanCh:
			if !ok {
				// A closed channel is ready forever, so leaving it in the
				// select spins this loop at 100% of a core for as long as the
				// kiosk is up — every scan ends, so that is always. Nil blocks
				// instead; F5 puts a live channel back.
				scanCh = nil
				continue
			}
			state.Mu.Lock()
			state.HandleScanEvent(ev)
			state.Mu.Unlock()

		// ── Input events ─────────────────────────────────────────────────────
		case ev := <-input.Events():
			state.Mu.Lock()
			inSession := state.Screen == ScreenSession && session != nil
			current := session
			if !inSession {
				handleInput(ev, state, scan, cfg, fb, &ctx, &cancelScan, &scanCh, &session, maxRows)
				state.Dirty = true
			}
			state.Mu.Unlock()

			// Session input forwards to the RDP socket, so it must not run
			// while the UI lock is held.
			if inSession {
				handleSessionInput(ev, state, current, input)
			}

		// ── Render tick ──────────────────────────────────────────────────────
		case <-ticker.C:
			state.Mu.Lock()
			// The connecting screen redraws every tick regardless: its spinner
			// has to animate, and it is the one screen whose exit is driven by
			// a background goroutine rather than by an event on this loop.
			if state.Screen != ScreenSession && (state.Dirty || state.Screen == ScreenConnecting) {
				spinTick++
				state.SpinnerTick = spinTick / 4
				mx, my := input.MousePos()
				Render(fb, backBuf, state, mx, my)
				state.Dirty = false
			}
			state.Mu.Unlock()
		}
	}
}

// handleInput processes an InputEvent in non-session screens.
// Callers must hold state.Mu.
func handleInput(
	ev inputdev.InputEvent,
	state *UIState,
	scan domain.Scanner,
	cfg config.Config,
	fb framebuffer.Device,
	ctx *context.Context,
	cancelScan *context.CancelFunc,
	scanCh *<-chan domain.ScanEvent,
	session **SessionState,
	maxRows int,
) {
	if ev.Type == inputdev.EvMouseMove {
		state.MouseX = ev.MouseX
		state.MouseY = ev.MouseY
		return
	}

	if ev.Type == inputdev.EvMouseButton && ev.Pressed && ev.Button == 1 {
		handleMouseClick(ev.MouseX, ev.MouseY, state, fb, session)
		return
	}

	if ev.Type != inputdev.EvKey || !ev.Pressed {
		return
	}

	// F2 cycles the UI language from any non-session screen. Handled before the
	// per-screen switch so it also works while the credential modal is open —
	// F2 produces no rune, so it never collides with text entry.
	if ev.KeyCode == inputdev.KeyF3 {
		inputdev.NextLayout()
		state.Dirty = true
		return
	}

	if ev.KeyCode == inputdev.KeyF2 {
		inputdev.SetLayout(inputdev.LayoutForLanguage(string(i18n.Next())))
		return
	}

	switch state.Screen {
	case ScreenDiscovery:
		handleDiscoveryKey(ev, state, scan, ctx, cancelScan, scanCh, maxRows)
	case ScreenModal:
		handleModalKey(ev, state, fb, session)
	}
}

func handleDiscoveryKey(
	ev inputdev.InputEvent,
	state *UIState,
	scan domain.Scanner,
	ctx *context.Context,
	cancelScan *context.CancelFunc,
	scanCh *<-chan domain.ScanEvent,
	maxRows int,
) {
	switch ev.KeyCode {
	case inputdev.KeyUp:
		state.MoveSelection(-1, maxRows)
	case inputdev.KeyDown:
		state.MoveSelection(1, maxRows)
	case inputdev.KeyPageUp:
		state.MoveSelection(-maxRows, maxRows)
	case inputdev.KeyPageDown:
		state.MoveSelection(maxRows, maxRows)
	case inputdev.KeyEnter:
		if len(state.Hosts) > 0 {
			state.OpenModalFor()
			state.Transition(ScreenModal)
		}
	case inputdev.KeyF5:
		(*cancelScan)()
		newCtx, newCancel := context.WithCancel(context.Background())
		*ctx = newCtx
		*cancelScan = newCancel
		state.Hosts = nil
		state.ScanDone = false
		state.ScanProgress = 0
		state.ErrorMsg = ""
		cidr, _ := network.DetectCIDR()
		*scanCh = scan.Start(newCtx, []string{cidr})
	}
}

func handleModalKey(
	ev inputdev.InputEvent,
	state *UIState,
	fb framebuffer.Device,
	session **SessionState,
) {
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		state.Modal = ModalState{}
		state.Transition(ScreenDiscovery)

	case inputdev.KeyTab:
		state.Modal.FocusIdx = (state.Modal.FocusIdx + 1) % 5

	case inputdev.KeyEnter:
		if state.Modal.FocusIdx == 4 {
			state.Transition(ScreenDiscovery)
		} else {
			go connect(state, fb, session)
		}

	case inputdev.KeyBackspace:
		idx := state.Modal.FocusIdx
		if idx < 3 {
			r := []rune(state.Modal.Fields[idx])
			if len(r) > 0 {
				state.Modal.Fields[idx] = string(r[:len(r)-1])
			}
		}

	default:
		if ev.Rune != 0 && state.Modal.FocusIdx < 3 {
			state.Modal.Fields[state.Modal.FocusIdx] += string(ev.Rune)
		}
	}
}

// handleSessionInput forwards input to the live RDP session. It must be called
// without state.Mu held: SendKey and friends write to the network socket.
func handleSessionInput(
	ev inputdev.InputEvent,
	state *UIState,
	session *SessionState,
	input *inputdev.Reader,
) {
	// Ctrl+Alt+End disconnects.
	if ev.Type == inputdev.EvKey && ev.KeyCode == inputdev.KeyEnd &&
		input.CtrlDown() && input.AltDown() {
		disconnect(state, session)
		return
	}

	if session.Client == nil {
		return
	}

	switch ev.Type {
	case inputdev.EvKey:
		_ = session.Client.SendKey(ev.KeyCode, ev.Pressed)
	case inputdev.EvMouseMove:
		_ = session.Client.SendMouse(ev.MouseX, ev.MouseY, 0)
		// RDP leaves the pointer to the client: the server moves its own but
		// never paints it into the screen updates, so without this the user is
		// pushing an invisible mouse around the remote desktop.
		if session.Writer != nil {
			session.Writer.MoveCursor(ev.MouseX, ev.MouseY)
		}
	case inputdev.EvMouseButton:
		if ev.Pressed {
			session.Client.SendMouseDown(ev.Button, ev.MouseX, ev.MouseY)
		} else {
			session.Client.SendMouseUp(ev.Button, ev.MouseX, ev.MouseY)
		}
	}
}

// handleMouseClick processes a left click. Callers must hold state.Mu.
func handleMouseClick(
	mx, my int,
	state *UIState,
	fb framebuffer.Device,
	session **SessionState,
) {
	switch state.Screen {
	case ScreenDiscovery:
		// Hit-testing goes through the same layout the renderer used, so a
		// click always lands on the row the user actually sees.
		rowIdx := layoutDiscoveryFor(fb.Bounds(), state.Hosts).rowAt(mx, my)
		if rowIdx < 0 {
			return
		}
		absIdx := state.ScrollOffset + rowIdx
		if absIdx >= 0 && absIdx < len(state.Hosts) {
			state.SelectedIdx = absIdx
			state.OpenModalFor()
			state.Transition(ScreenModal)
		}

	case ScreenModal:
		l := layoutModal(fb.Bounds())
		p := image.Pt(mx, my)

		// Clicking a field focuses it, so the dialog is usable with the mouse
		// alone rather than only by tabbing.
		for i, f := range l.Fields {
			if p.In(f) {
				state.Modal.FocusIdx = i
				return
			}
		}
		switch {
		case p.In(l.Connect):
			state.Modal.FocusIdx = 3
			go connect(state, fb, session)
		case p.In(l.Cancel):
			state.Transition(ScreenDiscovery)
		}
	}
}

// connect establishes the RDP session. It runs on its own goroutine and takes
// state.Mu itself; the network calls deliberately happen outside the lock so the
// render loop keeps running while the connection is being set up.
func connect(state *UIState, fb framebuffer.Device, session **SessionState) {
	state.Mu.Lock()
	host := state.SelectedHost()
	if host == nil {
		state.Mu.Unlock()
		return
	}

	creds := rdp.Credentials{
		Username: state.Modal.Fields[0],
		Password: state.Modal.Fields[1],
		Domain:   state.Modal.Fields[2],
	}
	addr := host.AddrRDP()
	selected := *host

	state.Transition(ScreenConnecting)
	state.Modal.Error = ""
	state.Mu.Unlock()

	client, err := rdp.New(addr, creds, fb.Width(), fb.Height())
	if err != nil {
		state.Mu.Lock()
		state.Modal.Error = rdpErrToMessage(err)
		state.Transition(ScreenModal)
		state.Mu.Unlock()
		return
	}

	writer := &rdp.FrameWriter{FB: fb}
	state.Mu.Lock()
	// Only now, with the session up: an account that was rejected is not one to
	// offer back the next time.
	state.RememberCredential(addr, creds.Username, creds.Domain)
	*session = &SessionState{
		Host:      selected,
		Client:    client,
		Writer:    writer,
		Connected: true,
	}
	state.Transition(ScreenSession)
	mx, my := state.MouseX, state.MouseY
	state.Mu.Unlock()

	// Show the pointer straight away rather than waiting for the first mouse
	// movement.
	writer.MoveCursor(mx, my)

	// RDP frame rendering loop (blocks until connection closes).
	for frame := range client.Frames() {
		writer.Write(frame)
	}

	// Connection closed.
	state.Mu.Lock()
	*session = nil
	state.Transition(ScreenDiscovery)
	state.ErrorMsg = i18n.T(i18n.Disconnected)
	state.Mu.Unlock()
}

func disconnect(state *UIState, session *SessionState) {
	if session != nil && session.Client != nil {
		_ = session.Client.Close()
	}
	state.Mu.Lock()
	state.Transition(ScreenDiscovery)
	state.ErrorMsg = i18n.T(i18n.Disconnected)
	state.Mu.Unlock()
}

// rdpErrToMessage maps a connection error onto a localised, user-facing message.
func rdpErrToMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "timeout"):
		return i18n.T(i18n.ErrTimeout)
	case strings.Contains(lower, "refused"):
		return i18n.T(i18n.ErrRefused)
	case strings.Contains(lower, "auth"),
		strings.Contains(lower, "logon"),
		strings.Contains(lower, "credential"):
		return i18n.T(i18n.ErrAuthFailed)
	}
	// Truncate on a rune boundary — localised or server-supplied text is not
	// guaranteed to be ASCII.
	if r := []rune(msg); len(r) > 60 {
		msg = string(r[:60]) + "..."
	}
	return msg
}
