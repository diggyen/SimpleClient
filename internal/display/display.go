// Package display is what the kiosk knows of its screens: the modes each
// connected display reports, whether it said what it is at all, and the
// resolutions offered when it is set by hand (F11).
//
// A display tells the graphics card what it is through its EDID. One that
// does not — a VGA cable or adapter without the DDC lines, a KVM switch, a
// monitor that is still asleep — gets the kernel's guesses instead, none of
// them above 1024x768, and the kiosk ran at 1024x768 on a 1600x900 screen.
// The resolution set by hand reaches the kernel as video= (see
// build/slot.cfg), which makes up the timings of a mode the display did not
// list; the session engine takes that mode (engine/kms.c).
package display

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Mode is a resolution.
type Mode struct{ W, H int }

func (m Mode) String() string {
	if m.W == 0 || m.H == 0 {
		return ""
	}
	return strconv.Itoa(m.W) + "x" + strconv.Itoa(m.H)
}

// Parse reads "1600x900", the way the boot environment keeps a resolution.
// Anything else, or a size no display has, is not one.
func Parse(s string) (Mode, bool) {
	ws, hs, ok := strings.Cut(s, "x")
	if !ok {
		return Mode{}, false
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w < 640 || h < 480 || w > 7680 || h > 4320 {
		return Mode{}, false
	}
	return Mode{w, h}, true
}

// Output is a connected display.
type Output struct {
	Name  string // the connector, "VGA-1", "HDMI-A-1"
	EDID  bool   // whether the display said what it is
	Modes []Mode // the ones it can be driven at, the kernel's order, no repeats
}

var drmPath = "/sys/class/drm"

// Outputs lists the connected displays the GPU's own driver drives. On the
// firmware framebuffer — safe graphics — there are none.
func Outputs() []Output {
	dirs, _ := filepath.Glob(filepath.Join(drmPath, "card*-*"))
	sort.Strings(dirs)
	var out []Output
	for _, d := range dirs {
		status, err := os.ReadFile(filepath.Join(d, "status"))
		if err != nil || strings.TrimSpace(string(status)) != "connected" {
			continue
		}
		name := filepath.Base(d)
		if _, after, ok := strings.Cut(name, "-"); ok {
			name = after
		}
		o := Output{Name: name}
		if edid, err := os.ReadFile(filepath.Join(d, "edid")); err == nil && len(edid) > 0 {
			o.EDID = true
		}
		modes, _ := os.ReadFile(filepath.Join(d, "modes"))
		for _, line := range strings.Fields(string(modes)) {
			// "1920x1080i" is interlaced: never chosen.
			m, ok := Parse(line)
			if ok && !contains(o.Modes, m) {
				o.Modes = append(o.Modes, m)
			}
		}
		out = append(out, o)
	}
	return out
}

// Common is what is offered besides the modes a display reports: the
// resolutions of the desk monitors a kiosk is likely to be put on, for the
// display that reports none of its own.
var Common = []Mode{
	{1920, 1080}, {1920, 1200}, {1680, 1050}, {1600, 900}, {1440, 900},
	{1366, 768}, {1280, 1024}, {1280, 800}, {1280, 720}, {1024, 768},
}

// Choices is the resolutions to offer: the ones the displays report and the
// common ones, largest first, each once.
func Choices(outs []Output) []Mode {
	var all []Mode
	for _, o := range outs {
		for _, m := range o.Modes {
			if !contains(all, m) {
				all = append(all, m)
			}
		}
	}
	for _, m := range Common {
		if !contains(all, m) {
			all = append(all, m)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		ai, aj := all[i].W*all[i].H, all[j].W*all[j].H
		if ai != aj {
			return ai > aj
		}
		return all[i].W > all[j].W
	})
	return all
}

// Reported reports whether one of the displays lists m among its modes.
func Reported(outs []Output, m Mode) bool {
	for _, o := range outs {
		if contains(o.Modes, m) {
			return true
		}
	}
	return false
}

// Blind reports whether a connected display did not say what it is.
func Blind(outs []Output) bool {
	for _, o := range outs {
		if !o.EDID {
			return true
		}
	}
	return false
}

func contains(list []Mode, m Mode) bool {
	for _, x := range list {
		if x == m {
			return true
		}
	}
	return false
}
