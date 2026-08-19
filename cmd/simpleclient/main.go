//go:build linux

// Command simpleclient is the SimpleClient kiosk entry point.
//
// It draws straight to the Linux framebuffer and reads raw evdev input — there
// is no X11 or Wayland underneath it. It is started by /init inside the
// initramfs (see build/init) and is expected to run forever; when it does exit,
// init restarts it after a short pause.
package main

import (
	"fmt"
	"os"

	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/rdp"
	"github.com/diggyen/SimpleClient/internal/scanner"
	"github.com/diggyen/SimpleClient/internal/ui"
)

func main() {
	cfg := config.Load()
	// Language and keyboard layout are set together. Picking Türkçe from the
	// GRUB menu and then finding the keyboard still on a US keymap is the same
	// surprise as picking it with F2 and finding that, and the boot path used
	// to have the bug the F2 path does not.
	lang := cfg.Lang()
	i18n.Set(lang)
	inputdev.SetLayout(inputdev.LayoutForLanguage(string(lang)))
	rdp.Debug = cfg.RDPDebug

	// The framebuffer is the only hard requirement: without it there is nothing
	// to draw on and no way to tell the user anything.
	fb, err := framebuffer.Open(cfg.FBDevice)
	if err != nil {
		fatalf("framebuffer: %v", err)
	}
	defer fb.Close()

	kbd, mouse := resolveInputDevices(cfg)

	// Say which nodes were picked. When a kiosk comes up with a working mouse
	// and a dead keyboard there is nothing on screen to explain it, and this
	// line on the serial console is the difference between a guess and a
	// diagnosis. -kbd and -mouse override it.
	warnf("input: keyboard=%s mouse=%s", orNone(kbd), orNone(mouse))

	if kbd == "" && mouse == "" {
		// Not fatal — the discovery screen still renders and reports what it
		// finds, which is far more useful than a boot loop with no output.
		warnf("no keyboard or mouse found; the UI will be read-only")
	}

	input, err := inputdev.New(kbd, mouse, fb.Width(), fb.Height())
	if err != nil {
		fatalf("input devices: %v", err)
	}
	defer input.Close()

	scan := scanner.New(cfg.MaxWorkers, cfg.ScanTimeout)

	// Run never returns.
	ui.Run(fb, input, scan, cfg)
}

// resolveInputDevices returns the keyboard and mouse evdev paths, auto-detecting
// whichever was not given explicitly. A device that cannot be found is reported
// and left empty; inputdev.New simply skips it.
func resolveInputDevices(cfg config.Config) (kbd, mouse string) {
	kbd = cfg.KbdDevice
	if kbd == "" {
		detected, err := inputdev.DetectKeyboard()
		if err != nil {
			warnf("keyboard auto-detect: %v", err)
		}
		kbd = detected
	}

	mouse = cfg.MouseDevice
	if mouse == "" {
		detected, err := inputdev.DetectMouse()
		if err != nil {
			warnf("mouse auto-detect: %v", err)
		}
		mouse = detected
	}
	return kbd, mouse
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "simpleclient: warning: "+format+"\n", args...)
}

// fatalf reports a startup failure and exits non-zero. init will restart us,
// so the message needs to say enough to diagnose the loop from the console.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "simpleclient: fatal: "+format+"\n", args...)
	os.Exit(1)
}
