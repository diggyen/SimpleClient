package ui

import (
	"image"
	"log"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/display"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
)

// DisplayState is the dialog that sets the resolution (F11), and the question
// the boot that tries a new one asks: can it be seen?
//
// A display that does not say what it is (no EDID) is driven at 1024x768 at
// most, whatever it can do; the resolution set here reaches the kernel at the
// next boot (bootenv.TryVideo). That boot asks whether to keep it, and goes
// back to the one before by restarting when no one answers: a resolution the
// display cannot show leaves no one to answer.
type DisplayState struct {
	// Confirm is the question on a boot trying a resolution; otherwise the
	// dialog is the list to choose from.
	Confirm bool

	// The list: the displays as they are now, the resolutions offered — ""
	// first, the displays' own — and the one chosen.
	Outputs []display.Output
	Choices []string
	Sel     int
	// Now is the resolution the screen is at.
	Now string

	// The question: the resolution tried, and when it goes unanswered.
	Mode  string
	Until time.Time
	Left  int // the seconds the question says are left
	Focus int // displayFocusKeep or displayFocusBack

	// Going is set once a restart is on its way; the dialog only says so.
	Going bool
	Err   string

	lastClick    time.Time
	lastClickRow int
}

// The question's buttons. Keep has the focus: anyone who can read the
// question can see the screen.
const (
	displayFocusKeep = iota
	displayFocusBack
)

// displayConfirmFor is how long the question waits for an answer.
var displayConfirmFor = 20 * time.Second

// The boot environment and the displays; tests replace them.
var (
	tryVideo     = bootenv.TryVideo
	keepVideo    = bootenv.KeepVideo
	probeOutputs = display.Outputs
)

// openDisplay shows the list. Callers must hold state.Mu.
func openDisplay(state *UIState, screen image.Rectangle) {
	outs := probeOutputs()
	d := DisplayState{Outputs: outs, Choices: []string{""}, lastClickRow: -1}
	for _, m := range display.Choices(outs) {
		d.Choices = append(d.Choices, m.String())
	}
	d.Now = display.Mode{W: screen.Dx(), H: screen.Dy()}.String()
	for i, c := range d.Choices {
		if c == state.Boot.Video {
			d.Sel = i
		}
	}
	state.Display = d
	state.Transition(ScreenDisplay)
}

// openDisplayConfirm asks whether to keep the resolution this boot is
// trying. Callers must hold state.Mu.
func openDisplayConfirm(state *UIState, now time.Time) {
	state.Display = DisplayState{
		Confirm: true,
		Mode:    state.Boot.Video,
		Until:   now.Add(displayConfirmFor),
		Left:    int(displayConfirmFor / time.Second),
		Focus:   displayFocusKeep,
	}
	state.Transition(ScreenDiscovery)
	state.Transition(ScreenDisplay)
}

// displayLabel names a resolution for the dialog: "" is the displays' own.
func displayLabel(mode string) string {
	if mode == "" {
		return i18n.T(i18n.DisplayAuto)
	}
	return mode
}

// handleDisplayKey drives the dialog. Callers must hold state.Mu.
func handleDisplayKey(ev inputdev.InputEvent, state *UIState) {
	d := &state.Display
	if d.Going {
		return
	}
	if d.Confirm {
		switch ev.KeyCode {
		case inputdev.KeyTab, inputdev.KeyLeft, inputdev.KeyRight:
			d.Focus = 1 - d.Focus
		case inputdev.KeyEsc:
			displayBack(state)
		case inputdev.KeyEnter:
			if d.Focus == displayFocusKeep {
				displayKeep(state)
			} else {
				displayBack(state)
			}
		}
		return
	}
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		state.Transition(ScreenDiscovery)
	case inputdev.KeyUp:
		d.Sel = clampInt(d.Sel-1, 0, len(d.Choices)-1)
	case inputdev.KeyDown:
		d.Sel = clampInt(d.Sel+1, 0, len(d.Choices)-1)
	case inputdev.KeyPageUp:
		d.Sel = clampInt(d.Sel-displayRows, 0, len(d.Choices)-1)
	case inputdev.KeyPageDown:
		d.Sel = clampInt(d.Sel+displayRows, 0, len(d.Choices)-1)
	case inputdev.KeyEnter:
		displayApply(state)
	}
}

// handleDisplayClick is the dialog's mouse: a click picks a resolution, a
// second one on it soon after applies it. Callers must hold state.Mu.
func handleDisplayClick(state *UIState, screen image.Rectangle, pt image.Point) {
	d := &state.Display
	if d.Going {
		return
	}
	l := layoutDisplay(screen, state)
	if d.Confirm {
		for i, b := range l.Buttons {
			if pt.In(b) {
				d.Focus = i
				if i == displayFocusKeep {
					displayKeep(state)
				} else {
					displayBack(state)
				}
				return
			}
		}
		return
	}
	for i, r := range l.Rows {
		if !pt.In(r) {
			continue
		}
		row := l.First + i
		at := clickClock()
		double := row == d.lastClickRow && at.Sub(d.lastClick) <= doubleClick
		d.Sel = row
		if double {
			d.lastClick = time.Time{}
			displayApply(state)
		} else {
			d.lastClick, d.lastClickRow = at, row
		}
		return
	}
}

// displayApply has the next boot try the resolution chosen, and restarts
// into it. The one already set closes the dialog. Callers must hold state.Mu.
func displayApply(state *UIState) {
	d := &state.Display
	// On the firmware framebuffer for want of the GPU's driver, the next
	// boot would be too, and a resolution cannot be seen there: the driver
	// comes first (F4), as the dialog says.
	if state.DriverOff {
		return
	}
	mode := d.Choices[d.Sel]
	if mode == state.Boot.Video && !state.Boot.VideoTry {
		state.Transition(ScreenDiscovery)
		return
	}
	d.Going, d.Err = true, ""
	log.Printf("display: restarting to try %q, as asked at the kiosk", mode)
	boot := state.Boot
	// The boot environment is on a partition to mount: not under the lock.
	go func() {
		if err := tryVideo(boot, mode); err != nil {
			log.Printf("display: %q cannot be tried: %v", mode, err)
			state.Mu.Lock()
			state.Display.Going, state.Display.Err = false, err.Error()
			state.Dirty = true
			state.Mu.Unlock()
			return
		}
		time.Sleep(powerDelay)
		restart()
	}()
}

// displayKeep keeps the resolution on trial, and goes on to where the kiosk
// starts. Callers must hold state.Mu.
func displayKeep(state *UIState) {
	mode := state.Display.Mode
	log.Printf("display: keeping %q", mode)
	boot := state.Boot
	state.Boot.VideoTry = false
	state.Display = DisplayState{}
	state.Transition(ScreenDiscovery)
	state.startAuto(time.Now())
	go func() {
		if err := keepVideo(boot, mode); err != nil {
			log.Printf("display: %q cannot be kept: %v", mode, err)
			state.Mu.Lock()
			state.flash(i18n.Tf(i18n.DisplayFailed, err.Error()), time.Now())
			state.Mu.Unlock()
		}
	}()
}

// displayBack restarts into the resolution kept before the try: GRUB has
// already cleared the try. Callers must hold state.Mu.
func displayBack(state *UIState) {
	log.Printf("display: going back from %q", state.Display.Mode)
	state.Display.Going = true
	state.Dirty = true
	time.AfterFunc(powerDelay, restart)
}

// displayTick counts the question down, and goes back when it runs out.
// Callers must hold state.Mu.
func displayTick(state *UIState, now time.Time) {
	d := &state.Display
	if state.Screen != ScreenDisplay || !d.Confirm || d.Going {
		return
	}
	if !now.Before(d.Until) {
		displayBack(state)
		return
	}
	// Drawn again only when the seconds shown change.
	if left := int((d.Until.Sub(now) + time.Second - 1) / time.Second); left != d.Left {
		d.Left = left
		state.Dirty = true
	}
}
