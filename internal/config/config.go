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
	flag.BoolVar(&cfg.RDPDebug, "rdp-debug", false,
		"Log the full RDP handshake to stderr. Diagnostic only: the RDP library "+
			"prints the password in clear text at this level.")
	flag.Parse()
	return cfg
}

// Lang resolves the configured language, falling back to the default when the
// value is empty or unrecognised.
func (c Config) Lang() i18n.Lang {
	lang, _ := i18n.Parse(c.Language)
	return lang
}
