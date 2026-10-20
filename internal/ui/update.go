package ui

import (
	"context"
	"errors"
	"image"
	"log"
	"math/rand/v2"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/update"
	"github.com/diggyen/SimpleClient/internal/version"
)

// UpdatePhase is where the update dialog is.
type UpdatePhase int

const (
	UpdateIdle     UpdatePhase = iota // showing what the last check found
	UpdateChecking                    // asking the release server
	UpdateRunning                     // downloading into the other slot
	UpdateRestart                     // written; restarting into it
)

// UpdateState is what a disk install knows of updates, and the update dialog
// (F9) that shows it.
type UpdateState struct {
	Phase UpdatePhase
	// Latest is the newest release, as the last check that got an answer
	// found it, at CheckedAt; Checked says whether one has.
	Latest    update.Release
	Checked   bool
	CheckedAt time.Time
	// Failed is a release that was tried and did not start on this machine.
	Failed string
	// Tried is the release the last update had this boot try, until a key
	// is pressed: the server list says how it went. TriedOK is whether this
	// boot runs it; if not, it did not start, and this is the version the
	// machine went back to.
	Tried   string
	TriedOK bool
	// Err is why the last check or update the dialog started did not work.
	Err string

	Done, Total int64 // the download, in bytes; Total -1 while unknown
	Focus       int   // updateFocusMain or updateFocusClose
	cancel      context.CancelFunc
}

const (
	updateFocusMain  = iota // Check, or Update once there is one
	updateFocusClose        // Close, or Cancel while downloading
)

// Available reports whether the last check found a release newer than this
// build, with an update to install it by.
func (u *UpdateState) Available() bool {
	return u.newer() && u.Latest.Sum != ""
}

// newer reports whether the last check found a release newer than this
// build, with an update or without.
func (u *UpdateState) newer() bool {
	return u.Checked && update.Newer(u.Latest.Tag, version.Version)
}

// The release server, the update and the restart; tests replace them.
var (
	latestRelease = update.Latest
	installUpdate = update.Install
	restart       = func() { _ = exec.Command("sh", "-c", "sync; reboot -f").Start() }
)

// Checks on a disk install: the first a few minutes after start-up, once the
// network has had the time to come up, then daily, give or take an hour, so a
// room of kiosks switched on together does not keep asking all at once. A
// check only asks. Installing is for someone at the kiosk to choose, never in
// the middle of their session.
var (
	firstCheck  = 2 * time.Minute
	checkEvery  = 24 * time.Hour
	checkSpread = 2 * time.Hour
	retryCheck  = 30 * time.Minute
	// recheckAfter is how old the last check may be for opening the dialog
	// not to start another.
	recheckAfter = 10 * time.Minute
)

// startUpdates settles the last update and starts the daily checks, on a disk
// install or a USB stick that updates. Callers must hold state.Mu.
func startUpdates(state *UIState) {
	if !state.Boot.Updatable() {
		return
	}
	u := &state.Update
	u.Tried, u.TriedOK, u.Failed = update.Settle(state.Boot)
	switch {
	case u.Tried != "" && u.TriedOK:
		log.Printf("update: %s is up, from slot %s", u.Tried, state.Boot.Slot)
	case u.Tried != "":
		log.Printf("update: %s did not start on this machine; back on %s", u.Tried, version.Version)
	}
	go func() {
		time.Sleep(firstCheck)
		for {
			err := checkUpdate(state)
			wait := checkEvery - checkSpread/2 + rand.N(checkSpread)
			if err != nil {
				wait = retryCheck
			}
			time.Sleep(wait)
		}
	}()
}

// checkUpdate asks the release server for its newest release and records the
// answer. It takes state.Mu itself.
func checkUpdate(state *UIState) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rel, err := latestRelease(ctx)
	if errors.Is(err, update.ErrNoUpdate) {
		err = nil
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()
	u := &state.Update
	if err != nil {
		log.Printf("update: check: %v", err)
	} else {
		u.Latest, u.Checked, u.CheckedAt = rel, true, time.Now()
		log.Printf("update: %s here, %s the latest", version.Version, rel.Tag)
	}
	state.Dirty = true
	return err
}

// openUpdate shows the update dialog, checking unless a check has only just
// answered. Callers must hold state.Mu.
func openUpdate(state *UIState) {
	u := &state.Update
	u.Phase, u.Err, u.Focus = UpdateIdle, "", updateFocusMain
	state.Transition(ScreenUpdate)
	if !u.Checked || time.Since(u.CheckedAt) > recheckAfter {
		checkNow(state)
	}
}

// checkNow checks in the background, for the dialog. Callers must hold
// state.Mu.
func checkNow(state *UIState) {
	u := &state.Update
	u.Phase, u.Err = UpdateChecking, ""
	go func() {
		err := checkUpdate(state)
		state.Mu.Lock()
		defer state.Mu.Unlock()
		if u.Phase != UpdateChecking {
			return
		}
		u.Phase = UpdateIdle
		if err != nil {
			u.Err = i18n.Tf(i18n.UpdateCheckFailed, updateReason(err))
		}
	}()
}

// handleUpdateKey drives the dialog. Callers must hold state.Mu.
func handleUpdateKey(ev inputdev.InputEvent, state *UIState) {
	u := &state.Update
	switch u.Phase {
	case UpdateRunning:
		if ev.KeyCode == inputdev.KeyEsc || ev.KeyCode == inputdev.KeyEnter {
			cancelUpdate(state)
		}
		return
	case UpdateRestart:
		return
	}
	switch ev.KeyCode {
	case inputdev.KeyEsc:
		state.Transition(ScreenDiscovery)
	case inputdev.KeyTab, inputdev.KeyLeft, inputdev.KeyRight:
		u.Focus = 1 - u.Focus
	case inputdev.KeyEnter:
		if u.Focus == updateFocusClose {
			state.Transition(ScreenDiscovery)
		} else {
			updateMain(state)
		}
	}
}

// handleUpdateClick is the dialog's mouse. Callers must hold state.Mu.
func handleUpdateClick(state *UIState, screen image.Rectangle, p image.Point) {
	u := &state.Update
	l := layoutUpdate(screen)
	switch {
	case p.In(l.Close):
		switch u.Phase {
		case UpdateRunning:
			cancelUpdate(state)
		case UpdateIdle, UpdateChecking:
			state.Transition(ScreenDiscovery)
		}
	case p.In(l.Main) && u.Phase == UpdateIdle:
		u.Focus = updateFocusMain
		updateMain(state)
	}
}

// updateMain is the main button: Update once there is one, Check until then.
// Callers must hold state.Mu.
func updateMain(state *UIState) {
	u := &state.Update
	switch {
	case u.Phase != UpdateIdle:
	case u.Available():
		startUpdate(state)
	default:
		checkNow(state)
	}
}

// startUpdate installs the newest release into the other slot in the
// background, then restarts into it. Callers must hold state.Mu.
func startUpdate(state *UIState) {
	u := &state.Update
	rel, boot := u.Latest, state.Boot
	ctx, cancel := context.WithCancel(context.Background())
	u.Phase, u.Err, u.Done, u.Total, u.cancel = UpdateRunning, "", 0, -1, cancel
	log.Printf("update: %s to %s, into slot %s", version.Version, rel.Tag, boot.Other())

	go func() {
		defer cancel()
		logged := int64(0)
		err := installUpdate(ctx, rel, boot, func(done, total int64) {
			// The console gets the count too: it is what tells a download
			// that is crawling from one that has stopped.
			if done-logged >= 32<<20 || done == total {
				log.Printf("update: %d of %d MB", done>>20, total>>20)
				logged = done
			}
			state.Mu.Lock()
			u.Done, u.Total = done, total
			state.Dirty = true
			state.Mu.Unlock()
		})

		state.Mu.Lock()
		defer state.Mu.Unlock()
		state.Dirty = true
		u.cancel = nil
		if err != nil {
			log.Printf("update: %v", err)
			u.Phase, u.Err = UpdateIdle, i18n.Tf(i18n.UpdateFailed, updateReason(err))
			return
		}
		log.Printf("update: %s is in slot %s; restarting", rel.Tag, boot.Other())
		u.Phase = UpdateRestart
		time.AfterFunc(2*time.Second, restart)
	}()
}

// cancelUpdate stops the download; the other slot is left empty. Callers
// must hold state.Mu.
func cancelUpdate(state *UIState) {
	if c := state.Update.cancel; c != nil {
		c()
	}
}

// updateReason says why a check or an update did not work, the way the
// dialog puts it.
func updateReason(err error) string {
	var uerr *url.Error
	switch {
	case errors.Is(err, context.Canceled):
		return i18n.T(i18n.UpdateCancelled)
	case errors.Is(err, update.ErrDamaged):
		return i18n.T(i18n.UpdateErrDamaged)
	case errors.Is(err, update.ErrStalled):
		return i18n.T(i18n.UpdateErrStalled)
	case errors.Is(err, update.ErrUnproven):
		return i18n.T(i18n.UpdateErrUnproven)
	case errors.Is(err, update.ErrInsecure):
		return i18n.T(i18n.UpdateErrInsecure)
	case errors.Is(err, update.ErrNoRoom):
		return i18n.T(i18n.UpdateErrNoRoom)
	case errors.As(err, &uerr):
		return i18n.Tf(i18n.UpdateErrNetwork, uerr.Err.Error())
	}
	return strings.TrimPrefix(err.Error(), "update: ")
}

// updateBanner is what the server list says, after an update, about how it
// went: up and running, or back on this version. ok tells which.
func updateBanner(u *UpdateState) (msg string, ok bool) {
	switch {
	case u.Tried == "":
		return "", false
	case u.TriedOK:
		return i18n.Tf(i18n.UpdateNoticeOK, u.Tried), true
	}
	return i18n.Tf(i18n.UpdateNoticeFailed, u.Tried, version.Version), false
}

// updateNote is what the foot of the server list says about updates: that
// there is a new release, unless it is one that would not start here, and the
// key that installs it, F11 on a USB stick.
func updateNote(u *UpdateState, live bool) string {
	if u.Available() && u.Latest.Tag != u.Failed {
		key := "F9"
		if live {
			key = "F11"
		}
		return i18n.Tf(i18n.UpdateNew, u.Latest.Tag, key)
	}
	return ""
}
