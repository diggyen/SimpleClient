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
	"runtime/debug"

	"github.com/diggyen/SimpleClient/internal/audio"
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
	if cfg.ColorDepth == 16 || cfg.ColorDepth == 24 {
		rdp.ColorDepth = cfg.ColorDepth
	}

	// The session decodes and discards megabytes of tiles a second. The heap
	// it keeps live is small, so letting it grow further between collections
	// costs a little memory and saves the collector running constantly on a
	// slow CPU, which is what showed up as stutter.
	debug.SetGCPercent(400)

	// The framebuffer is the only hard requirement: without it there is nothing
	// to draw on and no way to tell the user anything.
	fb, err := framebuffer.Open(cfg.FBDevice)
	if err != nil {
		fatalf("framebuffer: %v", err)
	}
	defer fb.Close()

	// Explicit -kbd/-mouse (kbddev=/mousedev= on the GRUB line) pin the nodes,
	// for the one machine where autodetection picks the wrong device. Otherwise
	// every keyboard and pointer is read, including ones plugged in later.
	var input *inputdev.Reader
	if cfg.KbdDevice != "" || cfg.MouseDevice != "" {
		input, err = inputdev.New(cfg.KbdDevice, cfg.MouseDevice, fb.Width(), fb.Height())
		if err != nil {
			fatalf("input devices: %v", err)
		}
	} else {
		input = inputdev.NewAuto(fb.Width(), fb.Height())
	}
	defer input.Close()

	// Say which nodes were picked. When a kiosk comes up with a working mouse
	// and a dead keyboard there is nothing on screen to explain it, and this
	// line on the serial console is the difference between a guess and a
	// diagnosis.
	warnf("input: %v", input.Devices())

	// Sound: open every card's mixer and hand sessions a sink. It is set up even
	// with no card present, so a USB headset plugged in later just works.
	go audio.InitMixer()
	rdp.Audio = audio.NewSink(&audio.Selector{})
	for _, d := range audio.Scan() {
		warnf("sound: %s", d)
	}

	scan := scanner.New(cfg.MaxWorkers, cfg.ScanTimeout)

	// Run never returns.
	ui.Run(fb, input, scan, cfg)
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
