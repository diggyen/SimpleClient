package config

import (
	"flag"
	"time"
)

// Config holds all runtime configuration for SimpleClient.
type Config struct {
	FBDevice    string
	KbdDevice   string
	MouseDevice string
	ScanTimeout time.Duration
	MaxWorkers  int
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
	flag.Parse()
	return cfg
}
