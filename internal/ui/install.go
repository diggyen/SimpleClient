package ui

import (
	"log"
	"os/exec"
	"strings"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/install"
	"github.com/diggyen/SimpleClient/internal/usb"
	"github.com/diggyen/SimpleClient/internal/version"
)

// InstallPhase is where the install-to-disk dialog is.
type InstallPhase int

const (
	InstallPick    InstallPhase = iota // choosing the disk
	InstallConfirm                     // "everything on it will be erased"
	InstallRunning                     // the installer is working
	InstallDone                        // installed; Enter restarts
	InstallFailed                      // the installer stopped with an error
)

// InstallState is the install-to-disk dialog.
type InstallState struct {
	Phase InstallPhase
	Disks []install.Disk
	Sel   int
	Table string // "gpt" or "mbr", as install.Table chose for this machine

	Step, Total int
	StepName    string
	Err         string
}

// openInstall shows the install dialog with the disks there are now.
// Callers must hold state.Mu.
func openInstall(state *UIState) {
	state.Install = InstallState{Disks: install.Disks(), Table: install.Table()}
	state.Transition(ScreenInstall)
}

// handleInstallKey drives the install dialog. Callers must hold state.Mu.
func handleInstallKey(ev inputdev.InputEvent, state *UIState) {
	in := &state.Install
	switch in.Phase {
	case InstallPick:
		switch ev.KeyCode {
		case inputdev.KeyUp:
			if in.Sel > 0 {
				in.Sel--
			}
		case inputdev.KeyDown:
			if in.Sel < len(in.Disks)-1 {
				in.Sel++
			}
		case inputdev.KeyEnter:
			if len(in.Disks) > 0 {
				in.Phase = InstallConfirm
			}
		case inputdev.KeyEsc:
			state.Transition(ScreenDiscovery)
		}

	case InstallConfirm:
		// A letter, not Enter: wiping a disk takes a deliberate key, and one
		// that a stray Enter from the previous step cannot press.
		if ev.Rune != 0 && strings.EqualFold(string(ev.Rune), i18n.T(i18n.InstallLetter)) {
			startInstall(state)
		} else if ev.KeyCode == inputdev.KeyEsc {
			in.Phase = InstallPick
		}

	case InstallDone:
		if ev.KeyCode == inputdev.KeyEnter {
			// reboot -f restarts straight away rather than through init, which
			// here is a shell script that would only start the kiosk again.
			_ = exec.Command("sh", "-c", "sync; reboot -f").Start()
		}

	case InstallFailed:
		if ev.KeyCode == inputdev.KeyEnter || ev.KeyCode == inputdev.KeyEsc {
			openInstall(state)
		}
	}
}

// startInstall runs the installer for the chosen disk in the background and
// feeds its progress to the dialog. Callers must hold state.Mu.
func startInstall(state *UIState) {
	in := &state.Install
	disk := in.Disks[in.Sel]
	opt := install.Options{
		Table:   in.Table,
		Lang:    string(i18n.Current()),
		Kernel:  state.Boot.Kernel,
		Version: version.Version,
	}
	in.Phase = InstallRunning
	in.Step, in.Total, in.StepName, in.Err = 0, 6, "", ""

	// A USB disk may have drives mounted for sessions; the installer is about
	// to write over it.
	usb.HoldDisk(disk.Name)
	log.Printf("install: %s (%s), %s", disk.Path(), disk.Label(), opt.Table)
	boot := state.Boot
	go func() {
		if boot.Live && boot.Slot != "" {
			// An updated stick installs the release it runs, from its slot,
			// not the one it was written with.
			opt.Slot = boot.Slot
			if env, err := bootenv.Read(boot); err == nil {
				opt.Slots, _ = bootenv.LiveSlots(env)
			}
		}
		err := install.Run(disk, opt, func(step, total int, name string) {
			log.Printf("install: step %d/%d %s", step, total, name)
			state.Mu.Lock()
			state.Install.Step, state.Install.Total, state.Install.StepName = step, total, name
			state.Dirty = true
			state.Mu.Unlock()
		})
		state.Mu.Lock()
		if err != nil {
			log.Printf("install: failed: %v", err)
			state.Install.Phase = InstallFailed
			state.Install.Err = err.Error()
		} else {
			log.Printf("install: done")
			state.Install.Phase = InstallDone
		}
		state.Dirty = true
		state.Mu.Unlock()
	}()
}

// installStepText is what the dialog says during each installer step.
func installStepText(name string) string {
	switch name {
	case install.StepMedium:
		return i18n.T(i18n.InstallStepMedium)
	case install.StepPartition:
		return i18n.T(i18n.InstallStepPartition)
	case install.StepFormat:
		return i18n.T(i18n.InstallStepFormat)
	case install.StepCopy:
		return i18n.T(i18n.InstallStepCopy)
	case install.StepBootloader:
		return i18n.T(i18n.InstallStepBootloader)
	case install.StepFirmware:
		return i18n.T(i18n.InstallStepFirmware)
	}
	return i18n.T(i18n.InstallStepMedium)
}
