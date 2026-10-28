package ui

import (
	"image"
	"log"
	"os/exec"
	"slices"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
)

// PowerState is the dialog that restarts or switches off the kiosk (F4).
// Without it the way to do either was the power button or the plug, and a
// machine switched off while /init loads the GPU's driver is counted against
// that driver (see bootenv.GraphicsTry).
type PowerState struct {
	Focus int // the button with the focus: powerFocusRestart ... powerFocusDriver
	// Going is the choice made, once made: the dialog then only says so,
	// until the machine goes.
	Going int
	// Why is said instead of the choice when the kiosk restarts on its own.
	Why string
	// Driver offers to try the GPU's driver again: this boot is on the
	// firmware framebuffer because two boots died loading it, which may have
	// been the plug rather than the driver (bootenv.RetryGraphics).
	Driver bool
}

// The dialog's buttons. Cancel has the focus when it opens, so an Enter
// pressed out of habit closes it rather than the machine.
const (
	powerFocusRestart = iota
	powerFocusOff
	powerFocusCancel
	powerFocusDriver
)

// buttons is the dialog's buttons in focus order, left to right.
func (p *PowerState) buttons() []int {
	if p.Driver {
		return []int{powerFocusRestart, powerFocusOff, powerFocusDriver, powerFocusCancel}
	}
	return []int{powerFocusRestart, powerFocusOff, powerFocusCancel}
}

// move moves the focus by delta buttons, round the row.
func (p *PowerState) move(delta int) {
	b := p.buttons()
	i := slices.Index(b, p.Focus)
	p.Focus = b[(i+delta+len(b))%len(b)]
}

// powerNone is Going before a choice is made.
const powerNone = -1

// The switch off, and the next boot's try of the GPU's driver; tests replace
// them, as they do restart (update.go).
var (
	powerOff      = func() { _ = exec.Command("sh", "-c", "sync; poweroff -f").Start() }
	retryGraphics = bootenv.RetryGraphics
)

// powerDelay is how long the dialog says what is happening before it does.
var powerDelay = time.Second

// driverRestart carries RestartForDriver's request to the UI loop.
var driverRestart = make(chan struct{}, 1)

// driverRestartDelay is how long the kiosk says why it restarts on its own.
var driverRestartDelay = 4 * time.Second

// RestartForDriver has the kiosk restart once it is idle: on the server list
// or counting down to its startup server, never in a session or a dialog
// someone is using. It is for a boot left on the firmware framebuffer whose
// GPU the boot rules have just given its driver back (bootenv.MarkGood).
func RestartForDriver() {
	select {
	case driverRestart <- struct{}{}:
	default:
	}
}

// restartWhenIdle restarts for RestartForDriver if the kiosk is idle, and
// reports whether it did. Callers must hold state.Mu.
func restartWhenIdle(state *UIState) bool {
	if !state.driverPending || (state.Screen != ScreenDiscovery && state.Screen != ScreenAuto) {
		return false
	}
	state.driverPending = false
	state.Transition(ScreenDiscovery)
	state.Power = PowerState{Focus: powerFocusCancel, Going: powerFocusRestart, Why: i18n.T(i18n.PowerDriverRetry)}
	state.Transition(ScreenPower)
	state.Dirty = true
	log.Printf("power: restarting to give the GPU its driver back")
	time.AfterFunc(driverRestartDelay, restart)
	return true
}

// openPower shows the dialog. Callers must hold state.Mu.
func openPower(state *UIState) {
	state.Power = PowerState{Focus: powerFocusCancel, Going: powerNone, Driver: state.DriverOff}
	state.Transition(ScreenPower)
}

// handlePowerKey drives the dialog. Callers must hold state.Mu.
func handlePowerKey(ev inputdev.InputEvent, state *UIState) {
	p := &state.Power
	if p.Going != powerNone {
		return
	}
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		state.Transition(ScreenDiscovery)
	case inputdev.KeyTab, inputdev.KeyRight:
		p.move(1)
	case inputdev.KeyLeft:
		p.move(-1)
	case inputdev.KeyEnter:
		powerChoose(state, p.Focus)
	}
}

// handlePowerClick is the dialog's mouse. Callers must hold state.Mu.
func handlePowerClick(state *UIState, screen image.Rectangle, pt image.Point) {
	if state.Power.Going != powerNone {
		return
	}
	l := layoutPower(screen, &state.Power)
	for i, b := range l.Buttons {
		if pt.In(b) {
			state.Power.Focus = l.Kinds[i]
			powerChoose(state, l.Kinds[i])
			return
		}
	}
}

// powerChoose does what the button at focus says. Callers must hold state.Mu.
func powerChoose(state *UIState, focus int) {
	switch focus {
	case powerFocusRestart:
		log.Printf("power: restarting, as asked at the kiosk")
		state.Power.Going = focus
		time.AfterFunc(powerDelay, restart)
	case powerFocusOff:
		log.Printf("power: switching off, as asked at the kiosk")
		state.Power.Going = focus
		time.AfterFunc(powerDelay, powerOff)
	case powerFocusDriver:
		log.Printf("power: restarting to try the GPU's driver again, as asked at the kiosk")
		state.Power.Going = focus
		boot := state.Boot
		// The boot environment is on a partition to mount: not under the lock.
		go func() {
			if err := retryGraphics(boot); err != nil {
				log.Printf("power: the GPU's driver will not be tried: %v", err)
			}
			time.Sleep(powerDelay)
			restart()
		}()
	default:
		state.Transition(ScreenDiscovery)
	}
}
