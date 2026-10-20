// Package bootenv is the state the boot loader and the running kiosk share.
//
// GRUB keeps it in its environment block, a grubenv file on the boot medium:
// which kernel and which system slot to boot, and whether the last boot came
// up. GRUB marks every boot pending before it starts the kernel; the kiosk
// clears the mark once it is running, and records what it runs on. A mark
// still set at the next boot means the last one never got that far, and GRUB
// tries the other kernel — see build/grub.cfg and build/grub-disk.cfg.
//
// The display gets the same treatment, for the GPU's own driver: /init marks
// the adapters it is about to load a driver for, and clears the mark once
// there is a screen. A mark left behind sends those adapters to the firmware
// framebuffer from then on.
package bootenv

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// header opens every grubenv. The rest of the file is "name=value" lines and
// '#' padding: GRUB rewrites the file in place and never changes its length,
// which is what makes writing it from the boot loader safe on any file system.
const header = "# GRUB Environment Block\n"

// Env is the variables of a grubenv.
type Env map[string]string

// Parse decodes a grubenv.
func Parse(b []byte) (Env, error) {
	s := string(b)
	if !strings.HasPrefix(s, header) {
		return nil, errors.New("bootenv: not a GRUB environment block")
	}
	env := Env{}
	for _, line := range strings.Split(s[len(header):], "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}

// Encode lays the variables out as a grubenv of exactly size bytes. They are
// sorted, so the same variables always make the same file.
func (e Env) Encode(size int) ([]byte, error) {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(header)
	for _, k := range keys {
		v := e[k]
		// GRUB escapes these with a backslash; nothing written here needs them.
		if k == "" || strings.ContainsAny(k, "=\n\\") || strings.ContainsAny(v, "\n\\") {
			return nil, fmt.Errorf("bootenv: cannot store %q=%q", k, v)
		}
		b.WriteString(k + "=" + v + "\n")
	}
	if b.Len() > size {
		return nil, fmt.Errorf("bootenv: %d bytes of variables do not fit in %d", b.Len(), size)
	}
	return []byte(b.String() + strings.Repeat("#", size-b.Len())), nil
}

// Update sets variables in the grubenv at path, rewriting it in place at its
// own size, the way GRUB does.
func Update(path string, set map[string]string) error {
	return Modify(path, func(env Env) {
		for k, v := range set {
			env[k] = v
		}
	})
}

// Modify lets fn change the variables of the grubenv at path and, if it did,
// rewrites the file in place at its own size, the way GRUB does.
func Modify(path string, fn func(Env)) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	env, err := Parse(old)
	if err != nil {
		return err
	}
	before := maps.Clone(env)
	fn(env)
	if maps.Equal(before, env) {
		return nil
	}
	b, err := env.Encode(len(old))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(b, 0); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Boot describes how the running kiosk was started, from the kernel command
// line GRUB built.
type Boot struct {
	// Slot is "A" or "B" on a disk install, and on a USB stick that runs an
	// update (Live); "" when running from the files of the ISO, on a stick or
	// a CD.
	Slot string
	// Live is a USB stick that the ISO was written to, the kiosk running from
	// it, and its grubenv where it can keep updates: the boot menu of the ISO
	// says so whenever it found that grubenv (sc.live=1). Updates go into
	// directories of the stick (LiveSlots), and settings are where they are
	// on any stick, LiveDevice.
	Live bool
	// Kernel is "new" or "lts", the kernel GRUB chose.
	Kernel string
	// GfxSafe is the display adapters to leave on the firmware framebuffer:
	// a comma-separated list of GPU sets (see GPUs) whose own driver once
	// took a boot down, or "once" from the boot menu's safe-graphics entry.
	GfxSafe string
	// ESP is the UUID of a disk install's ESP, where its grubenv is. Every
	// install has the same labels, and a machine can have two disks with one
	// on; GRUB knows which it booted from. Empty on an install without UUIDs.
	ESP string
}

// Installed reports whether the kiosk runs from a disk install.
func (b Boot) Installed() bool { return b.Slot != "" && !b.Live }

// Updatable reports whether the kiosk can update itself where it runs from:
// a disk install, or a USB stick (Live).
func (b Boot) Updatable() bool { return b.Installed() || b.Live }

// Other is the system slot the kiosk is not running from, the one an update
// goes into: on a stick running the files of the ISO, slot A.
func (b Boot) Other() string {
	if b.Slot == "A" {
		return "B"
	}
	return "A"
}

var cmdlinePath = "/proc/cmdline"

// Current reads the running boot's parameters.
func Current() Boot {
	data, _ := os.ReadFile(cmdlinePath)
	var b Boot
	for _, f := range strings.Fields(string(data)) {
		if v, ok := strings.CutPrefix(f, "sc.slot="); ok {
			b.Slot = v
		}
		if v, ok := strings.CutPrefix(f, "sc.kernel="); ok {
			b.Kernel = v
		}
		if v, ok := strings.CutPrefix(f, "sc.gfxsafe="); ok {
			b.GfxSafe = v
		}
		if v, ok := strings.CutPrefix(f, "sc.esp="); ok {
			b.ESP = v
		}
		if f == "sc.live=1" {
			b.Live = true
		}
	}
	return b
}

// SafeGraphics reports whether this boot leaves the display on the firmware
// framebuffer rather than loading the GPU's own driver, on a machine whose
// display adapters are gpus: when the boot menu asked for it, or when these
// adapters took a boot down before.
func (b Boot) SafeGraphics(gpus string) bool {
	for _, s := range strings.Split(b.GfxSafe, ",") {
		if s == "once" || (s != "" && s == gpus) {
			return true
		}
	}
	return false
}

var pciPath = "/sys/bus/pci/devices"

// GPUs names the machine's display adapters the way the boot environment
// remembers them: vendor:device of each PCI display controller, sorted and
// joined by "+" — "8086:46a6", or "10de:2860+8086:a7a0" for two. It is the
// hardware that is remembered, not the machine: a USB stick moved to another
// computer tries that one's GPU driver afresh, and on one with the same GPU
// it knows better than to.
func GPUs() string {
	dirs, _ := os.ReadDir(pciPath)
	var ids []string
	for _, d := range dirs {
		read := func(name string) string {
			b, _ := os.ReadFile(filepath.Join(pciPath, d.Name(), name))
			return strings.TrimPrefix(strings.TrimSpace(string(b)), "0x")
		}
		if strings.HasPrefix(read("class"), "03") {
			ids = append(ids, read("vendor")+":"+read("device"))
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, "+")
}

// maxGPUSets bounds the remembered GPU sets, which share GRUB's 1 KiB
// environment block with everything else: a stick that has met more failing
// GPUs than this forgets the oldest.
const maxGPUSets = 6

// compactGPUs drops empty and repeated entries from a list of GPU sets —
// GRUB can only append to it — and keeps the newest maxGPUSets.
func compactGPUs(list string) string {
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s != "" && s != "once" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) > maxGPUSets {
		out = out[len(out)-maxGPUSets:]
	}
	return strings.Join(out, ",")
}

// GraphicsTry records, before the GPU's own driver is loaded, the display
// adapters it is about to drive. A boot that dies before GraphicsOK clears
// the mark — the driver hung or crashed the machine, or took the screen away
// without giving one back — makes GRUB add them to the adapters left on the
// firmware framebuffer, so the next boot comes up without that driver. It
// reports whether the mark was written; booted from a CD there is nowhere to
// write it.
func GraphicsTry(b Boot, gpus string) bool {
	return withEnv(b, func(env Env) { env["sc_gfxtry"] = gpus }) == nil
}

// GraphicsOK clears GraphicsTry's mark once the driver has brought up a
// screen.
func GraphicsOK(b Boot) error {
	return withEnv(b, func(env Env) { env["sc_gfxtry"] = "" })
}

// MarkGood records that this boot came up: the kernel it runs on is the one
// to keep using and, on a disk install or a stick that updates, so is the
// slot — which is what makes a freshly installed update permanent; on a stick
// that runs the files of the ISO, the slot is none. Booted from a CD there is
// nothing to write, and nothing is.
func MarkGood(b Boot) error {
	err := withEnv(b, func(env Env) {
		env["sc_kpending"] = "0"
		env["sc_gfxtry"] = ""
		if s, ok := env["sc_gfxsafe"]; ok {
			env["sc_gfxsafe"] = compactGPUs(s)
		}
		if b.Kernel == "new" || b.Kernel == "lts" {
			env["sc_kernel"] = b.Kernel
		}
		if b.Updatable() {
			env["sc_boot"] = b.Slot
		}
	})
	if errors.Is(err, errNoEnv) {
		return nil
	}
	return err
}

// Read returns the variables of the grubenv of the medium this boot came from.
func Read(b Boot) (Env, error) {
	var env Env
	err := withEnv(b, func(e Env) { env = maps.Clone(e) })
	return env, err
}

// TryOther has the next boot, and only that one, start the other slot, which
// an update has just been written to. GRUB clears the try before the slot's
// kernel even starts: a boot of it that never gets as far as MarkGood is
// followed by one of the slot it came from. See build/grub-disk.cfg, and on a
// stick the ISO's grub.cfg.
func TryOther(b Boot) error {
	if !b.Updatable() {
		return errNoEnv
	}
	return withEnv(b, func(env Env) { env["sc_try"] = b.Other() })
}

// SlotsLabel is the label of the partition a USB stick the ISO was written to
// as it is keeps a live kiosk's updates on, which the kiosk adds behind the
// ISO (install.AddLiveSlots). grubenv records it by its volume ID, sc_slots.
const SlotsLabel = "SC_SLOTS"

// ErrNoSlots is a stick the ISO was written to as it is that has no partition
// for updates yet.
var ErrNoSlots = errors.New("bootenv: the USB stick has no partition for updates yet")

// LiveSlots returns the FAT partition whose slots/A and slots/B directories
// hold a live kiosk's updates, by the grubenv env of its stick: the one
// recorded in sc_slots or, on a stick Rufus wrote in ISO mode, the one
// partition holding everything. A stick the ISO was written to as it is has
// none until the kiosk has added it: ErrNoSlots.
func LiveSlots(env Env) (string, error) {
	if uuid := env["sc_slots"]; uuid != "" {
		return findfs("UUID=" + uuid)
	}
	if _, err := FindLabel("SC_EFI"); err == nil {
		return "", ErrNoSlots
	}
	return FindLabel(RufusLabel)
}

// SetLiveSlots records the volume ID of the partition for updates the kiosk
// has added to the stick it runs from, b.
func SetLiveSlots(b Boot, uuid string) error {
	return withEnv(b, func(env Env) { env["sc_slots"] = uuid })
}

// SlotDevice returns the device of a disk install's system slot, "A" or "B",
// by the UUID recorded in its grubenv env, or by its label on an install
// without UUIDs.
func SlotDevice(env Env, slot string) (string, error) {
	return Find(env["sc_"+strings.ToLower(slot)], "SC_"+slot)
}

var errNoEnv = errors.New("bootenv: no boot environment on this medium")

// withEnv changes the grubenv of the medium this boot came from: on the ESP
// of a disk install, on the live partition of a USB stick.
func withEnv(b Boot, fn func(Env)) error {
	var dev, file string
	var err error
	if b.Installed() {
		dev, err = Find(b.ESP, "SC_ESP")
		file = "boot/grub/grubenv"
	} else {
		dev, err = LiveDevice()
		file = "grubenv"
	}
	if err != nil {
		return errNoEnv
	}
	return WithMounted(dev, "vfat", func(dir string) error {
		return Modify(filepath.Join(dir, file), fn)
	})
}

// Find returns the device holding the file system whose UUID is uuid or,
// when uuid is empty, the one labelled label.
func Find(uuid, label string) (string, error) {
	if uuid == "" {
		return FindLabel(label)
	}
	return findfs("UUID=" + uuid)
}

// FindLabel returns the device holding the file system labelled label.
func FindLabel(label string) (string, error) {
	return findfs("LABEL=" + label)
}

// RufusLabel is the label of the one FAT partition Rufus's ISO mode writes
// the ISO's files to: the ISO's volume ID, SIMPLECLIENT, cut to the 11
// characters FAT allows.
const RufusLabel = "SIMPLECLIEN"

// LiveDevice returns the FAT partition a kiosk running from a USB stick keeps
// its grubenv and settings on, both in the same places on either: SC_EFI on a
// stick the ISO was written to as it is (dd, Rufus's DD mode), the partition
// holding everything on one Rufus wrote in ISO mode.
func LiveDevice() (string, error) {
	if dev, err := FindLabel("SC_EFI"); err == nil {
		return dev, nil
	}
	return FindLabel(RufusLabel)
}

func findfs(what string) (string, error) {
	out, err := exec.Command("findfs", what).Output()
	dev := strings.TrimSpace(string(out))
	if err != nil || dev == "" {
		return "", fmt.Errorf("bootenv: no file system with %s", what)
	}
	return dev, nil
}
