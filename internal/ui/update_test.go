package ui

import (
	"context"
	"errors"
	"image"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/update"
	"github.com/diggyen/SimpleClient/internal/version"
)

// fakeUpdates stands in for the release server, the installer and the
// restart, and puts them back after the test.
type fakeUpdates struct {
	latest    update.Release
	latestErr error
	install   func(ctx context.Context, progress update.Progress) error
	restarted chan struct{}
}

func useFakeUpdates(t *testing.T, f *fakeUpdates) {
	t.Helper()
	oldL, oldI, oldR, oldV := latestRelease, installUpdate, restart, version.Version
	version.Version = "v0.7.0"
	f.restarted = make(chan struct{}, 1)
	latestRelease = func(context.Context) (update.Release, error) { return f.latest, f.latestErr }
	installUpdate = func(ctx context.Context, r update.Release, b bootenv.Boot, p update.Progress) error {
		return f.install(ctx, p)
	}
	restart = func() { f.restarted <- struct{}{} }
	t.Cleanup(func() { latestRelease, installUpdate, restart, version.Version = oldL, oldI, oldR, oldV })
}

func updateKey(state *UIState, code int) {
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if state.Screen == ScreenUpdate {
		handleUpdateKey(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Pressed: true}, state)
	} else if code == inputdev.KeyF9 && state.Boot.Installed() {
		openUpdate(state)
	}
}

// waitUpdate polls the state, under its lock, until cond holds.
func waitUpdate(t *testing.T, state *UIState, what string, cond func(u *UpdateState) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state.Mu.Lock()
		ok := cond(&state.Update)
		state.Mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func installedState() *UIState {
	return &UIState{Screen: ScreenDiscovery, Boot: bootenv.Boot{Slot: "A", Kernel: "new"}}
}

// F9 on a disk install opens the dialog, which checks straight away; a newer
// release turns Check into Update, and Update downloads it, then restarts.
func TestUpdate_CheckThenUpdate(t *testing.T) {
	f := &fakeUpdates{latest: update.Release{Tag: "v0.7.1", Sum: strings.Repeat("a", 64)}}
	f.install = func(ctx context.Context, p update.Progress) error {
		p(100<<20, 250<<20)
		return nil
	}
	useFakeUpdates(t, f)
	state := installedState()

	updateKey(state, inputdev.KeyF9)
	if state.Screen != ScreenUpdate {
		t.Fatalf("F9 on a disk install opened screen %v", state.Screen)
	}
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Checked })
	if !state.Update.Available() || updateNote(&state.Update, false) != i18n.Tf(i18n.UpdateNew, "v0.7.1", "F9") {
		t.Fatalf("v0.7.1 over v0.7.0 not offered: %+v", state.Update)
	}

	updateKey(state, inputdev.KeyEnter)
	waitUpdate(t, state, "the restart phase", func(u *UpdateState) bool { return u.Phase == UpdateRestart })
	if state.Update.Done != 100<<20 || state.Update.Total != 250<<20 {
		t.Fatalf("progress %d of %d", state.Update.Done, state.Update.Total)
	}
	select {
	case <-f.restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart after the update was written")
	}
}

// The same release as this one, or an older one, is nothing to update to:
// the button keeps checking.
func TestUpdate_UpToDate(t *testing.T) {
	f := &fakeUpdates{latest: update.Release{Tag: "v0.7.0", Sum: strings.Repeat("a", 64)}}
	f.install = func(context.Context, update.Progress) error {
		t.Error("installed the release that is running")
		return nil
	}
	useFakeUpdates(t, f)
	state := installedState()

	updateKey(state, inputdev.KeyF9)
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Checked })
	if msg, _ := updateStatus(&state.Update); msg != i18n.T(i18n.UpdateUpToDate) {
		t.Fatalf("status %q", msg)
	}
	updateKey(state, inputdev.KeyEnter)
	waitUpdate(t, state, "the second check", func(u *UpdateState) bool { return u.Phase == UpdateIdle })
	if updateNote(&state.Update, false) != "" {
		t.Fatal("a note for no new version")
	}
}

// A failed check says why, in the dialog's words.
func TestUpdate_CheckFails(t *testing.T) {
	f := &fakeUpdates{latestErr: &url.Error{Op: "Get", URL: "https://github.com", Err: errors.New("no such host")}}
	useFakeUpdates(t, f)
	state := installedState()

	updateKey(state, inputdev.KeyF9)
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle })
	want := i18n.Tf(i18n.UpdateCheckFailed, i18n.Tf(i18n.UpdateErrNetwork, "no such host"))
	if state.Update.Err != want || state.Update.Checked {
		t.Fatalf("got %q (checked %v), want %q", state.Update.Err, state.Update.Checked, want)
	}
}

// A server that is not HTTPS is told as that, also when it was a redirect,
// which the client hands back as a url.Error, and not as one out of reach.
func TestUpdateReason_Insecure(t *testing.T) {
	for _, err := range []error{
		update.ErrInsecure,
		&url.Error{Op: "Get", URL: "https://mirror/latest", Err: update.ErrInsecure},
	} {
		if got, want := updateReason(err), i18n.T(i18n.UpdateErrInsecure); got != want {
			t.Errorf("%v: got %q, want %q", err, got, want)
		}
	}
}

// Esc while downloading cancels; the dialog stays, saying so, and nothing
// restarts.
func TestUpdate_CancelWhileDownloading(t *testing.T) {
	f := &fakeUpdates{latest: update.Release{Tag: "v0.7.1", Sum: strings.Repeat("a", 64)}}
	started := make(chan struct{})
	f.install = func(ctx context.Context, p update.Progress) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	useFakeUpdates(t, f)
	state := installedState()

	updateKey(state, inputdev.KeyF9)
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Checked })
	updateKey(state, inputdev.KeyEnter)
	<-started
	updateKey(state, inputdev.KeyEsc)
	waitUpdate(t, state, "the cancel", func(u *UpdateState) bool { return u.Phase == UpdateIdle })
	if want := i18n.Tf(i18n.UpdateFailed, i18n.T(i18n.UpdateCancelled)); state.Update.Err != want {
		t.Fatalf("got %q, want %q", state.Update.Err, want)
	}
	if state.Screen != ScreenUpdate {
		t.Fatalf("cancelling left the dialog for screen %v", state.Screen)
	}
	select {
	case <-f.restarted:
		t.Fatal("restarted after a cancelled update")
	case <-time.After(50 * time.Millisecond):
	}
}

// A release that was tried and did not start here is not announced again;
// the dialog says what happened, and still lets it be tried.
func TestUpdate_ReleaseThatDidNotStart(t *testing.T) {
	f := &fakeUpdates{latest: update.Release{Tag: "v0.7.1", Sum: strings.Repeat("a", 64)}}
	useFakeUpdates(t, f)
	state := installedState()
	state.Update.Failed = "v0.7.1"

	updateKey(state, inputdev.KeyF9)
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Checked })
	if note := updateNote(&state.Update, false); note != "" {
		t.Fatalf("note %q for a release that did not start", note)
	}
	if msg, _ := updateStatus(&state.Update); msg != i18n.Tf(i18n.UpdateDidNotStart, "v0.7.1") {
		t.Fatalf("status %q", msg)
	}
	if !state.Update.Available() {
		t.Fatal("the release can no longer be tried")
	}
}

// The buttons work with the mouse: Update starts the update, Close leaves.
func TestUpdate_Mouse(t *testing.T) {
	f := &fakeUpdates{latest: update.Release{Tag: "v0.7.1", Sum: strings.Repeat("a", 64)}}
	f.install = func(context.Context, update.Progress) error { return errors.New("update: disk full") }
	useFakeUpdates(t, f)
	state := installedState()
	screen := image.Rect(0, 0, 1280, 720)
	l := layoutUpdate(screen)
	click := func(r image.Rectangle) {
		state.Mu.Lock()
		handleUpdateClick(state, screen, r.Min.Add(image.Pt(r.Dx()/2, r.Dy()/2)))
		state.Mu.Unlock()
	}

	updateKey(state, inputdev.KeyF9)
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Checked })
	click(l.Main)
	waitUpdate(t, state, "the update to fail", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Err != "" })
	if want := i18n.Tf(i18n.UpdateFailed, "disk full"); state.Update.Err != want {
		t.Fatalf("got %q, want %q", state.Update.Err, want)
	}
	click(l.Close)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("Close left screen %v", state.Screen)
	}
}

// The dialog fits on the smallest screen, in every phase and language.
func TestRenderUpdate_FitsEveryPhase(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })
	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, u := range []UpdateState{
			{},
			{Phase: UpdateChecking},
			{Checked: true, Latest: update.Release{Tag: "v9.9.9", Sum: "x"}},
			{Checked: true, Latest: update.Release{Tag: "v9.9.9"}, Err: strings.Repeat("a very long reason ", 20)},
			{Phase: UpdateRunning, Latest: update.Release{Tag: "v9.9.9"}, Done: 10 << 20, Total: 250 << 20},
			{Phase: UpdateRunning, Latest: update.Release{Tag: "v9.9.9"}, Done: 10 << 20, Total: -1},
			{Phase: UpdateRestart, Latest: update.Release{Tag: "v9.9.9"}},
		} {
			img := image.NewRGBA(image.Rect(0, 0, 640, 480))
			state := &UIState{Screen: ScreenUpdate, Update: u}
			renderUpdate(img, state)
			l := layoutUpdate(img.Bounds())
			if !l.Box.In(img.Bounds()) {
				t.Fatalf("lang=%s: the dialog %v does not fit 640x480", lang, l.Box)
			}
			if l.StatusY+updateStatusMax*(CharH+6) > l.Main.Min.Y {
				t.Fatalf("lang=%s: the status text runs into the buttons", lang)
			}
			if w := TextWidth(i18n.T(i18n.UpdateThis), false); l.X+w >= l.ValueX {
				t.Fatalf("lang=%s: the label runs into the version", lang)
			}
		}
	}
}

// After an update the server list says how it went, until the first key: up
// and running in green, or back on this version.
func TestUpdateBanner(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.Default) })
	oldV := version.Version
	version.Version = "v0.7.0"
	defer func() { version.Version = oldV }()

	u := &UpdateState{Tried: "v0.7.0", TriedOK: true}
	if msg, ok := updateBanner(u); !ok || msg != i18n.Tf(i18n.UpdateNoticeOK, "v0.7.0") {
		t.Fatalf("came up: got %q, %v", msg, ok)
	}
	u = &UpdateState{Tried: "v0.7.1"}
	if msg, ok := updateBanner(u); ok || msg != i18n.Tf(i18n.UpdateNoticeFailed, "v0.7.1", "v0.7.0") {
		t.Fatalf("went back: got %q, %v", msg, ok)
	}

	for _, lang := range i18n.Available() {
		i18n.Set(lang)
		for _, size := range []image.Point{{640, 480}, {800, 600}, {1280, 800}} {
			for _, errMsg := range []string{"", "Bağlantı kesildi"} {
				img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
				state := &UIState{Screen: ScreenDiscovery, ErrorMsg: errMsg, Update: UpdateState{Tried: "v0.7.1"}}
				renderDiscovery(img, state)
				// The banner's text is drawn in ColorText on its own fill: find
				// the fill's rows, and make sure they stay off the key hints.
				l := layoutDiscovery(img.Bounds())
				hints := l.Screen.Max.Y - hintsBottom
				for y := hints; y < size.Y; y++ {
					for x := 0; x < size.X; x++ {
						if img.RGBAAt(x, y) == ColorWarningBG {
							t.Fatalf("lang=%s %v error=%q: the banner runs into the key hints at y=%d", lang, size, errMsg, y)
						}
					}
				}
			}
		}
	}

	// A key on the list puts it away.
	state := installedState()
	state.Update.Tried = "v0.7.1"
	handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: inputdev.KeyDown, Pressed: true},
		state, nil, config.Config{}, framebuffer.NewMock(1280, 720), nil, nil, nil, nil, 20)
	if state.Update.Tried != "" {
		t.Fatal("the banner is still up after a key")
	}
}

// On a USB stick F9 still installs to disk, and F11 updates the stick; F11
// does nothing on a CD, or anywhere the kiosk cannot update itself.
func TestUpdate_F11OnAStick(t *testing.T) {
	f := &fakeUpdates{latest: update.Release{Tag: "v0.7.0", Sum: strings.Repeat("a", 64)}}
	useFakeUpdates(t, f)
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	var scanCh <-chan domain.ScanEvent
	key := func(state *UIState, code int) {
		state.Mu.Lock()
		defer state.Mu.Unlock()
		handleInput(inputdev.InputEvent{Type: inputdev.EvKey, KeyCode: code, Pressed: true},
			state, &mockScanner{}, config.Config{}, framebuffer.NewMock(1280, 720),
			&ctx, &cancel, &scanCh, nil, 20)
	}

	state := &UIState{Screen: ScreenDiscovery, CanInstall: true}
	key(state, inputdev.KeyF11)
	if state.Screen != ScreenDiscovery {
		t.Fatalf("F11 on a CD opened screen %v", state.Screen)
	}

	state = &UIState{Screen: ScreenDiscovery, CanInstall: true, Boot: bootenv.Boot{Live: true}}
	key(state, inputdev.KeyF9)
	if state.Screen != ScreenInstall {
		t.Fatalf("F9 on a stick opened screen %v, not the install dialog", state.Screen)
	}
	key(state, inputdev.KeyEsc)
	key(state, inputdev.KeyF11)
	if state.Screen != ScreenUpdate {
		t.Fatalf("F11 on a stick opened screen %v, not the update dialog", state.Screen)
	}
	waitUpdate(t, state, "the check", func(u *UpdateState) bool { return u.Phase == UpdateIdle && u.Checked })
}
