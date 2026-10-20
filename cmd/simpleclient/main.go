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
	"time"

	"github.com/diggyen/SimpleClient/internal/audio"
	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/config"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/install"
	"github.com/diggyen/SimpleClient/internal/rdp"
	"github.com/diggyen/SimpleClient/internal/scanner"
	"github.com/diggyen/SimpleClient/internal/ui"
	"github.com/diggyen/SimpleClient/internal/usb"
)

func main() {
	cfg := config.Load()
	if cfg.BootGraphics != "" {
		os.Exit(bootGraphics(cfg.BootGraphics))
	}
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
	dev, err := framebuffer.Open(cfg.FBDevice)
	if err != nil {
		fatalf("framebuffer: %v", err)
	}
	// With the GPU's driver the screen is the display's native mode; on 4K
	// the kiosk's screens are drawn at twice the size, pixel for pixel.
	scale := framebuffer.Scale(dev.Width(), dev.Height())
	fb := framebuffer.NewScaled(dev, scale)
	defer fb.Close()
	fmt.Fprintf(os.Stderr, "simpleclient: screen %dx%d, drawn at %dx%d\n",
		dev.Width(), dev.Height(), fb.Width(), fb.Height())

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

	// Whether the GPU decodes the sessions' video: found out now rather than
	// at the first session, which would wait for it, and shown on screen.
	go rdp.ProbeDecoder()

	// USB drives are mounted as they are plugged in, for sessions to offer the
	// server; the stick the kiosk boots from is not one of them.
	usb.SetMediumCheck(install.IsInstallMedium)
	usb.Startup()

	scan := scanner.New(cfg.MaxWorkers, cfg.ScanTimeout)

	// GRUB marks every boot pending. Once the kiosk has been up a while this
	// boot has proven itself on this machine, and its kernel — and on a disk
	// install its system slot — are the ones to keep booting.
	boot := bootenv.Current()
	warnf("boot: slot=%q kernel=%q", boot.Slot, boot.Kernel)
	go func() {
		time.Sleep(provenAfter)
		if err := bootenv.MarkGood(boot); err != nil {
			warnf("boot: recording a good boot failed: %v", err)
		}
	}()

	// Run never returns.
	ui.Run(fb, input, scan, cfg)
}

// bootGraphics is /init's half of choosing the display driver; see
// build/init and bootenv.GraphicsTry. It runs before the kiosk and exits.
func bootGraphics(step string) int {
	boot := bootenv.Current()
	gpus := bootenv.GPUs()
	switch step {
	case "mode":
		mode := "native"
		if boot.SafeGraphics(gpus) {
			mode = "safe"
		}
		fmt.Println(mode)
		fmt.Fprintf(os.Stderr, "simpleclient: display adapters %q, %s graphics\n", gpus, mode)
		return 0
	case "try":
		if gpus == "" || !bootenv.GraphicsTry(boot, gpus) {
			return 1
		}
		return 0
	case "ok":
		if err := bootenv.GraphicsOK(boot); err != nil {
			warnf("boot: clearing the display mark failed: %v", err)
			return 1
		}
		return 0
	}
	return 2
}

// provenAfter is how long the kiosk has to run before its boot counts as
// good: long enough that a driver crashing the machine shortly after start-up
// still sends the next boot to the other kernel.
const provenAfter = 20 * time.Second

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "simpleclient: warning: "+format+"\n", args...)
}

// fatalf reports a startup failure and exits non-zero. init will restart us,
// so the message needs to say enough to diagnose the loop from the console.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "simpleclient: fatal: "+format+"\n", args...)
	os.Exit(1)
}
