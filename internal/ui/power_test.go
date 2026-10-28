package ui

import (
	"image"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
)

// fakePower stands in for the restart and the switch off, and puts them back
// after the test.
func fakePower(t *testing.T) (restarted, off chan struct{}) {
	t.Helper()
	oldR, oldO, oldD := restart, powerOff, powerDelay
	restarted, off = make(chan struct{}, 1), make(chan struct{}, 1)
	restart = func() { restarted <- struct{}{} }
	powerOff = func() { off <- struct{}{} }
	powerDelay = 0
	t.Cleanup(func() { restart, powerOff, powerDelay = oldR, oldO, oldD })
	return restarted, off
}

func powerKey(state *UIState, code int) {
	handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Pressed: true},
		state, nil, config.Config{}, framebuffer.NewMock(1280, 800), nil, nil, nil, nil, 10)
}

// expect waits for one of the fakes to be called, or fails.
func expect(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s was not called", what)
	}
}

// never fails if a fake is called.
func never(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s was called", what)
	case <-time.After(50 * time.Millisecond):
	}
}

// F4 opens the dialog with Cancel focused: Enter straight away, or Esc,
// closes it and nothing happens to the machine.
func TestPower_OpensOnCancel(t *testing.T) {
	restarted, off := fakePower(t)
	for _, closeKey := range []int{inputdev.KeyEnter, inputdev.KeyEsc} {
		state := &UIState{Screen: ScreenDiscovery}
		powerKey(state, inputdev.KeyF4)
		if state.Screen != ScreenPower || state.Power.Focus != powerFocusCancel {
			t.Fatalf("F4: screen %v focus %d, want the dialog on Cancel", state.Screen, state.Power.Focus)
		}
		powerKey(state, closeKey)
		if state.Screen != ScreenDiscovery {
			t.Errorf("key %d: screen %v, want the server list", closeKey, state.Screen)
		}
	}
	never(t, restarted, "restart")
	never(t, off, "switch off")
}

// Tab from Cancel wraps round to Restart; Enter restarts, and the dialog
// then takes no more keys.
func TestPower_Restart(t *testing.T) {
	restarted, off := fakePower(t)
	state := &UIState{Screen: ScreenDiscovery}
	powerKey(state, inputdev.KeyF4)
	powerKey(state, inputdev.KeyTab)
	if state.Power.Focus != powerFocusRestart {
		t.Fatalf("focus %d after Tab, want Restart", state.Power.Focus)
	}
	powerKey(state, inputdev.KeyEnter)
	expect(t, restarted, "restart")
	if state.Power.Going != powerFocusRestart {
		t.Errorf("going %d, want Restart", state.Power.Going)
	}
	powerKey(state, inputdev.KeyEsc)
	if state.Screen != ScreenPower {
		t.Errorf("Esc after the choice left the dialog")
	}
	never(t, off, "switch off")
}

// Left from Cancel is Switch off.
func TestPower_SwitchOffByKey(t *testing.T) {
	restarted, off := fakePower(t)
	state := &UIState{Screen: ScreenDiscovery}
	powerKey(state, inputdev.KeyF4)
	powerKey(state, inputdev.KeyLeft)
	powerKey(state, inputdev.KeyEnter)
	expect(t, off, "switch off")
	never(t, restarted, "restart")
}

// The mouse alone: a click on the F4 hint opens the dialog, a click on
// Switch off switches off.
func TestPower_Mouse(t *testing.T) {
	restarted, off := fakePower(t)
	fb := framebuffer.NewMock(1280, 800)
	state := &UIState{Screen: ScreenDiscovery}

	hints, at := placeKeyHints(fb.Bounds(), state)
	var f4 image.Rectangle
	for i, h := range hints {
		if h[0] == "F4" {
			f4 = at[i]
		}
	}
	if f4.Empty() {
		t.Fatal("no F4 hint on a 1280px screen")
	}
	c := f4.Min.Add(f4.Size().Div(2))
	handleMouseClick(c.X, c.Y, state, fb, nil)
	if state.Screen != ScreenPower {
		t.Fatalf("screen %v after a click on the F4 hint, want the dialog", state.Screen)
	}

	b := layoutPower(fb.Bounds()).Buttons[powerFocusOff]
	c = b.Min.Add(b.Size().Div(2))
	handleMouseClick(c.X, c.Y, state, fb, nil)
	expect(t, off, "switch off")
	never(t, restarted, "restart")
}

// Every screen size the kiosk runs at has room for the three buttons, and
// their labels fit in them in every language.
func TestPower_Layout(t *testing.T) {
	for _, w := range []int{640, 800, 1024, 1920} {
		l := layoutPower(image.Rect(0, 0, w, 480))
		if !l.Box.In(image.Rect(0, 0, w, 480)) {
			t.Errorf("w=%d: the dialog %v is off screen", w, l.Box)
		}
		for i, b := range l.Buttons {
			if !b.In(l.Box) {
				t.Errorf("w=%d: button %d %v is outside the dialog %v", w, i, b, l.Box)
			}
		}
	}
	t.Cleanup(func() { i18n.Set(i18n.Default) })
	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, k := range []i18n.Key{i18n.ButtonRestart, i18n.ButtonSwitchOff, i18n.ButtonCancel} {
			if tw := TextWidth(i18n.T(k), false); tw > modalBtnW-16 {
				t.Errorf("lang=%s: %q is %dpx, wider than its button", lang, i18n.T(k), tw)
			}
		}
		for _, k := range []i18n.Key{i18n.PowerTitle, i18n.PowerRestarting, i18n.PowerSwitchingOff} {
			if tw := TextWidth(i18n.T(k), false); tw > powerW-2*modalPad-16 {
				t.Errorf("lang=%s: %q is %dpx, wider than the dialog", lang, i18n.T(k), tw)
			}
		}
		if n := len(wrapText(i18n.T(i18n.PowerQuestion), (powerW-2*modalPad)/CharW)); n > 2 {
			t.Errorf("lang=%s: the question takes %d lines, the dialog has room for 2", lang, n)
		}
	}
}
