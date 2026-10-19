package ui

import (
	"context"
	"errors"
	"image"
	"strings"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
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
	Host   domain.Host
	Client rdp.Session
	// Writer paints the built-in client's frames; nil when the engine runs
	// the session, since it draws the desktop itself.
	Writer    *rdp.FrameWriter
	Connected bool
	Error     string
}

// pointer is the part of the input reader a session needs: the engine can
// drive the display at another resolution than the console's, and the remote
// desktop can move the pointer.
type pointer interface {
	SetScreen(w, h int)
	Warp(x, y int)
	MousePos() (int, int)
}

// Run is the main kiosk loop. It never returns.
//
// All UIState access goes through state.Mu: the connect goroutine started from
// the credential modal mutates the same struct concurrently with this loop.
// Nothing under the lock performs I/O that can block indefinitely — rdp.New and
// the RDP frame loop both run outside it — so holding it across a render is safe.
func Run(fb framebuffer.Device, input *inputdev.Reader, scan domain.Scanner, cfg config.Config) {
	state := &UIState{}
	if input != nil {
		state.pointer = input
	}
	state.Boot = bootenv.Current()
	state.CanInstall = !state.Boot.Installed()
	if state.Boot.Installed() {
		state.SavePath = savedPath
		state.loadSaved(savedPath)
	}
	backBuf := image.NewRGBA(fb.Bounds())

	// The reader starts the pointer at the centre of the screen; mirror that so
	// the first session starts with its cursor somewhere sensible rather than
	// in the top-left corner.
	state.MouseX, state.MouseY = input.MousePos()

	if cfg.StaticHost != "" && !state.AddStaticHost(cfg.StaticHost) {
		state.ErrorMsg = "rdphost: " + cfg.StaticHost + "?"
	}

	var session *SessionState

	// Start initial scan.
	ctx, cancelScan := context.WithCancel(context.Background())
	cidr, _ := network.DetectCIDR()
	state.Subnet = cidr
	scanCh := scan.Start(ctx, []string{cidr})

	ticker := time.NewTicker(16 * time.Millisecond) // ~60 FPS
	netTicker := time.NewTicker(3 * time.Second)
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

		// ── Late network ─────────────────────────────────────────────────────
		// A NIC whose link or DHCP lease came up after boot leaves the first
		// scan with no subnet. Rather than waiting for someone to think of F5,
		// scan as soon as an address appears.
		case <-netTicker.C:
			state.Mu.Lock()
			if state.Subnet == "" && state.Screen == ScreenDiscovery {
				if cidr, _ := network.DetectCIDR(); cidr != "" {
					cancelScan()
					ctx, cancelScan = context.WithCancel(context.Background())
					state.Subnet = cidr
					state.ScanDone = false
					state.ScanProgress = 0
					scanCh = scan.Start(ctx, []string{cidr})
					state.Dirty = true
				}
			}
			state.Mu.Unlock()

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
	// F4 moves sound to the next output: speakers or headphones, the monitor,
	// a USB headset. Automatic choice is right most of the time; this is for
	// the rest.
	if ev.KeyCode == inputdev.KeyF4 {
		if rdp.Audio != nil {
			rdp.Audio.Sel.Cycle()
			rdp.Audio.Reselect()
		}
		state.Dirty = true
		return
	}

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
	case ScreenInstall:
		handleInstallKey(ev, state)
	case ScreenNetwork:
		handleNetKey(ev, state)
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
		// With nothing found, Enter still connects: to a server typed in.
		if len(state.Hosts) > 0 {
			state.OpenModalFor()
		} else {
			state.OpenModalManual()
		}
		state.Transition(ScreenModal)
	case inputdev.KeyF7:
		state.OpenModalManual()
		state.Transition(ScreenModal)
	case inputdev.KeyF8:
		openNetwork(state)
	case inputdev.KeyF9:
		if state.CanInstall {
			openInstall(state)
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
		state.Subnet = cidr
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
		state.Modal.FocusIdx = (state.Modal.FocusIdx + 1) % (state.Modal.fieldCount() + 2)

	case inputdev.KeyF6:
		state.Modal.toggleDomain()

	case inputdev.KeyEnter:
		if state.Modal.FocusIdx == state.Modal.focusCancel() {
			state.Transition(ScreenDiscovery)
		} else {
			go connect(state, fb, session)
		}

	case inputdev.KeyBackspace:
		idx := state.Modal.FocusIdx
		if idx < state.Modal.fieldCount() {
			r := []rune(state.Modal.Fields[idx])
			if len(r) > 0 {
				state.Modal.Fields[idx] = string(r[:len(r)-1])
			}
		}

	default:
		if ev.Rune != 0 && state.Modal.FocusIdx < state.Modal.fieldCount() {
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
	// Ctrl+Alt+End disconnects. Gated on the press: releases are forwarded now
	// too, and without this the combination would fire twice.
	if ev.Type == inputdev.EvKey && ev.Pressed && ev.KeyCode == inputdev.KeyEnd &&
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
	case inputdev.EvMouseWheel:
		session.Client.SendWheel(ev.Wheel, ev.MouseX, ev.MouseY)
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
		l := layoutModal(fb.Bounds(), state.Modal.fieldCount())
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
		case p.In(l.Domain):
			state.Modal.toggleDomain()
		case p.In(l.Connect):
			state.Modal.FocusIdx = state.Modal.focusConnect()
			go connect(state, fb, session)
		case p.In(l.Cancel):
			state.Transition(ScreenDiscovery)
		}

	case ScreenNetwork:
		n := &state.Net
		if n.Busy {
			return
		}
		l := layoutNet(fb.Bounds(), n.Static)
		p := image.Pt(mx, my)
		switch {
		case p.In(l.DHCP):
			n.Static, n.Focus = false, netFocusMode
		case p.In(l.Static):
			n.Static, n.Focus = true, netFocusMode
		case p.In(l.Save):
			n.Focus = netFocusSave
			saveNetwork(state)
		case p.In(l.Cancel):
			state.Transition(ScreenDiscovery)
		default:
			for i, f := range l.Fields {
				if p.In(f) && n.Static {
					n.Focus = i + 1
				}
			}
		}
	}
}

// connect establishes the RDP session. It runs on its own goroutine and takes
// state.Mu itself; the network calls deliberately happen outside the lock so the
// render loop keeps running while the connection is being set up.
func connect(state *UIState, fb framebuffer.Device, session **SessionState) {
	state.Mu.Lock()
	server := state.Modal.Fields[fieldServer]
	creds := rdp.Credentials{
		Username: state.Modal.Fields[fieldUser],
		Password: state.Modal.Fields[fieldPassword],
		Domain:   state.Modal.domain(),
	}
	state.Transition(ScreenConnecting)
	state.Modal.Error = ""
	state.Mu.Unlock()

	// A typed name is looked up here, outside the lock: DNS can take seconds.
	host, err := domain.ParseAddress(server)
	state.Mu.Lock()
	if err != nil {
		state.Modal.Error = i18n.T(i18n.ErrBadAddress)
		state.Modal.FocusIdx = fieldServer
		state.Transition(ScreenModal)
		state.Mu.Unlock()
		return
	}
	// The connecting screen names the selected host, and a server typed in by
	// hand joins the list, to be picked from it next time.
	state.SelectedIdx = state.AddHost(host)
	selected := state.Hosts[state.SelectedIdx]
	addr := selected.AddrRDP()
	state.Mu.Unlock()

	// The server maps scancodes to characters by the layout announced at
	// connect time, so it follows the one selected on the kiosk (F3).
	rdp.KeyboardLayout = rdp.LayoutID(string(inputdev.CurrentLayout()))

	client, err := dial(state, addr, creds, fb)
	if err != nil {
		state.Mu.Lock()
		state.Modal.Error = rdpErrToMessage(err)
		state.Transition(ScreenModal)
		state.Mu.Unlock()
		return
	}

	state.Mu.Lock()
	// Only now, with the session up: an account that was rejected is not one to
	// offer back the next time.
	state.saveConnection(selected, creds.Username, creds.Domain)
	state.Mu.Unlock()

	for {
		runSession(state, fb, session, selected, client)
		if !client.Failed() {
			break
		}
		// The session died on an error — the network dropped, the server
		// stopped answering. The desktop is still running on the server (xrdp
		// and Windows both keep a disconnected session), so put the user back
		// into it rather than dropping them on the host list to type the
		// password again.
		client = reconnect(state, addr, creds, fb)
		if client == nil {
			break
		}
	}

	// Connection closed.
	state.Mu.Lock()
	*session = nil
	state.Transition(ScreenDiscovery)
	state.ErrorMsg = i18n.T(i18n.Disconnected)
	state.Mu.Unlock()
}

// reconnectDelays is the back-off between reconnection attempts: long enough
// for a switch port or a Wi-Fi bridge to come back, short enough that a user
// staring at the screen is not left wondering.
var reconnectDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// reconnect retries a failed session, showing the connecting screen meanwhile.
// It returns nil once every attempt has failed.
func reconnect(state *UIState, addr string, creds rdp.Credentials, fb framebuffer.Device) rdp.Session {
	state.Mu.Lock()
	state.Transition(ScreenConnecting)
	state.Dirty = true
	state.Mu.Unlock()

	for _, d := range reconnectDelays {
		time.Sleep(d)
		if c, err := dial(state, addr, creds, fb); err == nil {
			return c
		}
	}
	return nil
}

// dial connects through the session engine, and through the built-in client
// where the engine cannot run: no engine in the image, no display KMS can
// drive (a firmware framebuffer on a machine whose GPU has no driver).
func dial(state *UIState, addr string, creds rdp.Credentials, fb framebuffer.Device) (rdp.Session, error) {
	var onPointer func(x, y int)
	if state.pointer != nil {
		onPointer = state.pointer.Warp
	}
	e, err := rdp.StartEngine(addr, creds, onPointer)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, rdp.ErrNoEngine) {
		return nil, err
	}
	c, err := rdp.New(addr, creds, fb.Width(), fb.Height())
	if err != nil {
		return nil, err
	}
	return c, nil
}

// runSession shows one live connection until it ends.
func runSession(state *UIState, fb framebuffer.Device, session **SessionState, host domain.Host, client rdp.Session) {
	builtin, isBuiltin := client.(*rdp.Client)
	var writer *rdp.FrameWriter
	if isBuiltin {
		writer = &rdp.FrameWriter{FB: fb}
	}
	state.Mu.Lock()
	*session = &SessionState{
		Host:      host,
		Client:    client,
		Writer:    writer,
		Connected: true,
	}
	state.Transition(ScreenSession)
	mx, my := state.MouseX, state.MouseY
	state.Mu.Unlock()

	if !isBuiltin {
		// The engine draws the desktop and the pointer itself, at the
		// display's own mode, which the pointer's range follows for the
		// session.
		if e, ok := client.(*rdp.Engine); ok && state.pointer != nil && e.Width > 0 && e.Height > 0 {
			state.pointer.SetScreen(e.Width, e.Height)
			defer state.pointer.SetScreen(fb.Width(), fb.Height())
			x, y := state.pointer.MousePos()
			_ = client.SendMouse(x, y, 0)
		}
		<-client.Done()
		return
	}

	// Show the pointer straight away rather than waiting for the first mouse
	// movement.
	writer.MoveCursor(mx, my)

	// RDP frame rendering loop (blocks until connection closes).
	for update := range builtin.Frames() {
		writer.WriteUpdate(update)
	}
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
	// The engine has already sorted its failures.
	var ee *rdp.EngineError
	if errors.As(err, &ee) {
		switch ee.Class {
		case "auth":
			return i18n.T(i18n.ErrAuthFailed)
		case "unreachable":
			return i18n.T(i18n.ErrTimeout)
		case "refused":
			return i18n.T(i18n.ErrRefused)
		}
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
