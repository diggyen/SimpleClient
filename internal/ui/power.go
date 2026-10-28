package ui

import (
	"image"
	"log"
	"os/exec"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
)

// PowerState is the dialog that restarts or switches off the kiosk (F4).
// Without it the way to do either was the power button or the plug, and a
// machine switched off while /init loads the GPU's driver is counted against
// that driver (see bootenv.GraphicsTry).
type PowerState struct {
	Focus int // powerFocusRestart, powerFocusOff or powerFocusCancel
	// Going is the choice made, once made: the dialog then only says so,
	// until the machine goes.
	Going int
	// Why is said instead of the choice when the kiosk restarts on its own.
	Why string
}

// The dialog's buttons, in focus order. Cancel has the focus when it opens,
// so an Enter pressed out of habit closes it rather than the machine.
const (
	powerFocusRestart = iota
	powerFocusOff
	powerFocusCancel
	powerButtons
)

// powerNone is Going before a choice is made.
const powerNone = -1

// The switch off; tests replace it, as they do restart (update.go).
var powerOff = func() { _ = exec.Command("sh", "-c", "sync; poweroff -f").Start() }

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
	state.Power = PowerState{Focus: powerFocusCancel, Going: powerNone}
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
		p.Focus = (p.Focus + 1) % powerButtons
	case inputdev.KeyLeft:
		p.Focus = (p.Focus + powerButtons - 1) % powerButtons
	case inputdev.KeyEnter:
		powerChoose(state, p.Focus)
	}
}

// handlePowerClick is the dialog's mouse. Callers must hold state.Mu.
func handlePowerClick(state *UIState, screen image.Rectangle, pt image.Point) {
	if state.Power.Going != powerNone {
		return
	}
	for i, b := range layoutPower(screen).Buttons {
		if pt.In(b) {
			state.Power.Focus = i
			powerChoose(state, i)
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
	default:
		state.Transition(ScreenDiscovery)
	}
}
