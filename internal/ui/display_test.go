package ui

import (
	"errors"
	"image"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/display"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
)

// blindVGA is a display that did not say what it is: the kernel's guesses.
var blindVGA = []display.Output{{Name: "VGA-1", Modes: []display.Mode{{W: 1024, H: 768}, {W: 800, H: 600}, {W: 640, H: 480}}}}

// fakeDisplay stands in for the displays, the boot environment and the
// restart, and reports what was tried and kept.
func fakeDisplay(t *testing.T, outs []display.Output, tryErr error) (tried, kept chan string, restarted chan struct{}) {
	t.Helper()
	restarted, _ = fakePower(t)
	oldT, oldK, oldP, oldC := tryVideo, keepVideo, probeOutputs, displayConfirmFor
	tried, kept = make(chan string, 1), make(chan string, 1)
	tryVideo = func(_ bootenv.Boot, mode string) error {
		tried <- mode
		return tryErr
	}
	keepVideo = func(_ bootenv.Boot, mode string) error {
		kept <- mode
		return nil
	}
	probeOutputs = func() []display.Output { return outs }
	t.Cleanup(func() { tryVideo, keepVideo, probeOutputs, displayConfirmFor = oldT, oldK, oldP, oldC })
	return tried, kept, restarted
}

func expectString(t *testing.T, ch chan string, want, what string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("%s %q, want %q", what, got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("nothing %s", what)
	}
}

func neverString(t *testing.T, ch chan string, what string) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("%s %q", what, got)
	case <-time.After(50 * time.Millisecond):
	}
}

func choiceIndex(d *DisplayState, mode string) int {
	for i, c := range d.Choices {
		if c == mode {
			return i
		}
	}
	return -1
}

// F11 lists the displays' own resolution first and then, largest first, the
// ones offered, with the one kept chosen; Esc closes it and nothing happens.
func TestDisplay_OpensOnTheResolutionKept(t *testing.T) {
	tried, _, restarted := fakeDisplay(t, blindVGA, nil)
	state := &UIState{Screen: ScreenDiscovery, Boot: bootenv.Boot{Slot: "A", Video: "1440x900"}}
	powerKey(state, inputdev.KeyF11)
	d := &state.Display
	if state.Screen != ScreenDisplay || d.Confirm {
		t.Fatalf("F11: screen %v, want the list", state.Screen)
	}
	if d.Choices[0] != "" || d.Choices[d.Sel] != "1440x900" || choiceIndex(d, "1600x900") < 0 {
		t.Fatalf("choices %q, chosen %d", d.Choices, d.Sel)
	}
	if d.Now != "1280x800" {
		t.Fatalf("the screen is at %q, want the framebuffer's 1280x800", d.Now)
	}
	powerKey(state, inputdev.KeyEsc)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("Esc: screen %v", state.Screen)
	}
	neverString(t, tried, "tried")
	never(t, restarted, "restart")
}

// Enter on another resolution has the next boot try it, and restarts.
func TestDisplay_ApplyTriesAndRestarts(t *testing.T) {
	tried, _, restarted := fakeDisplay(t, blindVGA, nil)
	state := &UIState{Screen: ScreenDiscovery, Boot: bootenv.Boot{Slot: "A"}}
	powerKey(state, inputdev.KeyF11)
	if state.Display.Sel != 0 {
		t.Fatalf("with none kept, %q is chosen", state.Display.Choices[state.Display.Sel])
	}
	for state.Display.Choices[state.Display.Sel] != "1600x900" {
		powerKey(state, inputdev.KeyDown)
	}
	powerKey(state, inputdev.KeyEnter)
	expectString(t, tried, "1600x900", "tried")
	expect(t, restarted, "restart")
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if !state.Display.Going {
		t.Fatal("the dialog does not say it restarts")
	}
}

// Enter on the resolution kept closes the dialog: there is nothing to try.
func TestDisplay_ApplyTheSameCloses(t *testing.T) {
	tried, _, restarted := fakeDisplay(t, blindVGA, nil)
	state := &UIState{Screen: ScreenDiscovery, Boot: bootenv.Boot{Slot: "A", Video: "1600x900"}}
	powerKey(state, inputdev.KeyF11)
	powerKey(state, inputdev.KeyEnter)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("screen %v, want the list", state.Screen)
	}
	neverString(t, tried, "tried")
	never(t, restarted, "restart")
}

// A boot environment that cannot be written — a CD — is said, and the
// kiosk stays up.
func TestDisplay_ApplyFailsOnScreen(t *testing.T) {
	tried, _, restarted := fakeDisplay(t, nil, errors.New("no boot environment"))
	state := &UIState{Screen: ScreenDiscovery}
	powerKey(state, inputdev.KeyF11)
	powerKey(state, inputdev.KeyDown)
	powerKey(state, inputdev.KeyEnter)
	expectString(t, tried, "1920x1200", "tried")
	never(t, restarted, "restart")
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if state.Display.Going || state.Display.Err == "" || state.Screen != ScreenDisplay {
		t.Fatalf("after the failure: %+v", state.Display)
	}
}

// A double click applies the resolution clicked; one click only picks it.
func TestDisplay_DoubleClickApplies(t *testing.T) {
	tried, _, restarted := fakeDisplay(t, blindVGA, nil)
	clock := time.Unix(1000, 0)
	old := clickClock
	clickClock = func() time.Time { return clock }
	t.Cleanup(func() { clickClock = old })

	state := &UIState{Screen: ScreenDiscovery}
	screen := image.Rect(0, 0, 1280, 800)
	openDisplay(state, screen)
	row := layoutDisplay(screen, state).Rows[2]
	pt := image.Pt(row.Min.X+20, row.Min.Y+5)
	handleDisplayClick(state, screen, pt)
	if state.Display.Sel != 2 {
		t.Fatalf("a click picks %d, want 2", state.Display.Sel)
	}
	neverString(t, tried, "tried on one click")
	clock = clock.Add(200 * time.Millisecond)
	handleDisplayClick(state, screen, pt)
	expectString(t, tried, state.Display.Choices[2], "tried")
	expect(t, restarted, "restart")
}

// A boot trying a resolution asks first; Enter keeps it and goes on.
func TestDisplay_ConfirmKeeps(t *testing.T) {
	_, kept, restarted := fakeDisplay(t, nil, nil)
	state := &UIState{Boot: bootenv.Boot{Slot: "A", Video: "1600x900", VideoTry: true}}
	now := time.Now()
	openDisplayConfirm(state, now)
	if state.Screen != ScreenDisplay || !state.Display.Confirm || state.Display.Left != 20 {
		t.Fatalf("screen %v %+v, want the question", state.Screen, state.Display)
	}
	powerKey(state, inputdev.KeyEnter)
	expectString(t, kept, "1600x900", "kept")
	if state.Screen != ScreenDiscovery || state.Boot.VideoTry {
		t.Fatalf("after keeping: screen %v boot %+v", state.Screen, state.Boot)
	}
	// The countdown is over with the question.
	displayTick(state, now.Add(time.Minute))
	never(t, restarted, "restart")
}

// Unanswered, the question goes back: the restart boots the resolution kept
// before, since GRUB has cleared the try.
func TestDisplay_ConfirmRunsOut(t *testing.T) {
	_, kept, restarted := fakeDisplay(t, nil, nil)
	state := &UIState{Boot: bootenv.Boot{Slot: "A", Video: "1600x900", VideoTry: true}}
	now := time.Now()
	openDisplayConfirm(state, now)

	state.Dirty = false
	displayTick(state, now.Add(1500*time.Millisecond))
	if state.Display.Left != 19 || !state.Dirty {
		t.Fatalf("1.5 s in: %d s left, dirty %v", state.Display.Left, state.Dirty)
	}
	state.Dirty = false
	displayTick(state, now.Add(1600*time.Millisecond))
	if state.Dirty {
		t.Fatal("drawn again with the same seconds left")
	}
	never(t, restarted, "restart")

	displayTick(state, now.Add(displayConfirmFor))
	expect(t, restarted, "restart")
	neverString(t, kept, "kept")
	if !state.Display.Going {
		t.Fatal("the question does not say it goes back")
	}
}

// Go back, by key or by mouse, restarts at once.
func TestDisplay_ConfirmGoBack(t *testing.T) {
	_, kept, restarted := fakeDisplay(t, nil, nil)
	state := &UIState{Boot: bootenv.Boot{Slot: "A", Video: "1600x900", VideoTry: true}}
	openDisplayConfirm(state, time.Now())
	powerKey(state, inputdev.KeyTab)
	powerKey(state, inputdev.KeyEnter)
	expect(t, restarted, "restart")

	state = &UIState{Boot: bootenv.Boot{Slot: "A", VideoTry: true}}
	openDisplayConfirm(state, time.Now())
	screen := image.Rect(0, 0, 1280, 800)
	b := layoutDisplay(screen, state).Buttons[displayFocusBack]
	handleDisplayClick(state, screen, b.Min.Add(image.Pt(5, 5)))
	expect(t, restarted, "restart")
	neverString(t, kept, "kept")
}

// The dialog fits the smallest screen in every language, with its rows,
// keys and buttons inside it.
func TestDisplay_FitsTheScreen(t *testing.T) {
	fakeDisplay(t, blindVGA, nil)
	t.Cleanup(func() { i18n.Set(i18n.Default) })
	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, size := range []image.Point{{640, 480}, {1024, 768}, {1920, 1080}} {
			screen := image.Rect(0, 0, size.X, size.Y)
			for _, st := range []*UIState{
				{SafeGfx: true, DriverOff: true, Boot: bootenv.Boot{Video: "1600x900"}},
				{Boot: bootenv.Boot{Video: "1600x900", VideoTry: true}},
			} {
				if st.Boot.VideoTry {
					openDisplayConfirm(st, time.Now())
				} else {
					openDisplay(st, screen)
				}
				l := layoutDisplay(screen, st)
				if !l.Box.In(screen) {
					t.Errorf("%s %v: box %v off screen", lang, size, l.Box)
				}
				for _, r := range append(l.Rows, l.Buttons...) {
					if !r.In(l.Box) {
						t.Errorf("%s %v: %v outside the box %v", lang, size, r, l.Box)
					}
				}
				img := image.NewRGBA(screen)
				renderDisplay(img, st)
			}
		}
	}
}

// The way in is on the server list's footer, kept on a narrow screen, and
// a click on it opens the dialog.
func TestDisplay_HintOpensIt(t *testing.T) {
	fakeDisplay(t, blindVGA, nil)
	screen := image.Rect(0, 0, 640, 480)
	state := &UIState{Screen: ScreenDiscovery}
	hints, at := placeKeyHints(screen, state)
	for i, h := range hints {
		if h[0] == "F11" {
			fb := framebuffer.NewMock(640, 480)
			c := at[i].Min.Add(image.Pt(2, 2))
			handleMouseClick(c.X, c.Y, state, fb, nil)
			if state.Screen != ScreenDisplay {
				t.Fatalf("a click on the hint: screen %v", state.Screen)
			}
			return
		}
	}
	t.Fatalf("no F11 hint at 640 px: %q", hints)
}

// On the firmware framebuffer for want of the driver, the boot that tries the
// resolution tries the driver too: it would be on the firmware's mode again
// otherwise. Even the resolution already kept is tried, for the driver's sake.
func TestDisplay_SafeGraphicsTriesTheDriverToo(t *testing.T) {
	for _, sel := range []int{0, 1} {
		tried, _, restarted := fakeDisplay(t, nil, nil)
		retried := make(chan struct{}, 1)
		old := retryGraphics
		retryGraphics = func(bootenv.Boot) error { retried <- struct{}{}; return nil }
		t.Cleanup(func() { retryGraphics = old })
		state := &UIState{Screen: ScreenDiscovery, SafeGfx: true, DriverOff: true}
		powerKey(state, inputdev.KeyF11)
		for range sel {
			powerKey(state, inputdev.KeyDown)
		}
		want := state.Display.Choices[sel]
		powerKey(state, inputdev.KeyEnter)
		if !state.Display.Going {
			t.Fatalf("sel %d: not going: %+v", sel, state.Display)
		}
		expectString(t, tried, want, "tried")
		expect(t, retried, "driver retry")
		expect(t, restarted, "restart")
	}
}

// Without the safe graphics, the driver is left as it is.
func TestDisplay_DriverLeftAloneOtherwise(t *testing.T) {
	tried, _, restarted := fakeDisplay(t, nil, nil)
	retried := make(chan struct{}, 1)
	old := retryGraphics
	retryGraphics = func(bootenv.Boot) error { retried <- struct{}{}; return nil }
	t.Cleanup(func() { retryGraphics = old })
	state := &UIState{Screen: ScreenDiscovery}
	powerKey(state, inputdev.KeyF11)
	powerKey(state, inputdev.KeyDown)
	powerKey(state, inputdev.KeyEnter)
	<-tried
	expect(t, restarted, "restart")
	never(t, retried, "driver retry")
}
