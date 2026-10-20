package config

import (
	"flag"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
)

// Config holds all runtime configuration for SimpleClient.
type Config struct {
	FBDevice    string
	KbdDevice   string
	MouseDevice string
	ScanTimeout time.Duration
	MaxWorkers  int
	Language    string
	RDPDebug    bool
	StaticHost  string
	ColorDepth  int
	// BootGraphics is set only by /init, which runs the binary for one step
	// of choosing the display driver, or for the boot splash, before it
	// starts the kiosk.
	BootGraphics string
}

// Load parses command-line flags and returns a Config.
// Sensible defaults are used when flags are absent.
func Load() Config {
	cfg := Config{}
	flag.StringVar(&cfg.FBDevice, "fb", "/dev/fb0", "Framebuffer device path")
	flag.StringVar(&cfg.KbdDevice, "kbd", "", "Keyboard evdev path (auto-detect if empty)")
	flag.StringVar(&cfg.MouseDevice, "mouse", "", "Mouse evdev path (auto-detect if empty)")
	flag.DurationVar(&cfg.ScanTimeout, "scan-timeout", 500*time.Millisecond, "TCP dial timeout per host")
	flag.IntVar(&cfg.MaxWorkers, "workers", 256, "Concurrent scan workers")
	flag.StringVar(&cfg.Language, "lang", string(i18n.Default),
		"Startup UI language: en or tr (toggle at runtime with F2)")
	flag.StringVar(&cfg.StaticHost, "host", "",
		"IP address to add to the host list without scanning. The kiosk only "+
			"discovers its own subnet; this is the one way to reach a server "+
			"beyond it. Set from GRUB with rdphost=<ip>.")
	flag.IntVar(&cfg.ColorDepth, "bpp", 16,
		"Session colour depth, 16 or 24. 16 halves the bitmap stream and decodes "+
			"fastest; 24 avoids banding in gradients. Set from GRUB with bpp=24.")
	flag.BoolVar(&cfg.RDPDebug, "rdp-debug", false,
		"Log the full RDP handshake to stderr. Diagnostic only: the RDP library "+
			"prints the password in clear text at this level.")
	flag.StringVar(&cfg.BootGraphics, "boot-graphics", "",
		"Run by /init before the kiosk, then exit: \"mode\" prints native or safe "+
			"(load the GPU's own driver, or keep the firmware framebuffer), \"try\" "+
			"marks the display adapters in the boot environment before their driver "+
			"loads, \"ok\" clears the mark once there is a screen. \"splash\" shows "+
			"the boot splash until it is sent SIGTERM.")
	flag.Parse()
	return cfg
}

// Lang resolves the configured language, falling back to the default when the
// value is empty or unrecognised.
func (c Config) Lang() i18n.Lang {
	lang, _ := i18n.Parse(c.Language)
	return lang
}
