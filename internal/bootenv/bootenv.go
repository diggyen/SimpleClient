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
// there is a screen. A mark left behind two boots in a row sends those
// adapters to the firmware framebuffer from then on.
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
	"strconv"
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
	// Slot is "A" or "B" on a disk install, "" when running from a USB stick
	// or a CD.
	Slot string
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
func (b Boot) Installed() bool { return b.Slot != "" }

// Other is the system slot a disk install is not running from, the one an
// update goes into.
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
// without giving one back, or the machine was switched off just then — makes
// GRUB give the driver one more boot (sc_gfxfail); a second boot in a row that
// dies there adds them to the adapters left on the firmware framebuffer, so
// the boots after it come up without that driver. It reports whether the mark
// was written; booted from a CD there is nowhere to write it.
func GraphicsTry(b Boot, gpus string) bool {
	return withEnv(b, func(env Env) { env["sc_gfxtry"] = gpus }) == nil
}

// GraphicsOK clears GraphicsTry's mark once the driver has brought up a
// screen, and the one failed boot GRUB may have counted against it.
func GraphicsOK(b Boot) error {
	return withEnv(b, func(env Env) {
		env["sc_gfxtry"] = ""
		env["sc_gfxfail"] = ""
	})
}

// loaderPath is a disk install's GRUB script as this release has it.
// sc-install copies it to the ESP, and an update brings a new one only here,
// into the slot: MarkGood copies it over the ESP's once the release has come
// up.
var loaderPath = "/usr/share/simpleclient/grub-disk.cfg"

// MarkGood records that this boot came up: the kernel it runs on is the one
// to keep using and, on a disk install, so is the slot — which is what makes
// a freshly installed update permanent. Booted from a CD there is nothing to
// write, and nothing is.
//
// On a disk install it also brings the ESP's GRUB script up to this release's.
// When that changes the boot loader's rules, the display adapters the old one
// left on the firmware framebuffer get their driver back on the next boot, to
// be judged by the new rules: before v0.7.6 one boot switched off while the
// driver loaded was enough to lose it for good. retry reports that this boot is
// one of those, on the firmware framebuffer for want of a driver it may now
// have: the kiosk restarts to give it the driver, rather than wait for someone
// to restart it.
func MarkGood(b Boot) (retry bool, err error) {
	gpus := GPUs()
	err = withMedium(b, func(dir, file string) error {
		var loader []byte
		if b.Installed() {
			loader = newLoader(filepath.Join(dir, "boot/grub/grub.cfg"))
		}
		err := Modify(filepath.Join(dir, file), func(env Env) {
			retry = markGood(env, b, gpus, loader != nil)
		})
		if err != nil || loader == nil {
			return err
		}
		return replaceFile(filepath.Join(dir, "boot/grub/grub.cfg"), loader)
	})
	if errors.Is(err, errNoEnv) {
		return false, nil
	}
	return retry, err
}

// safeRetryEvery is how many boots in a row a GPU spends on the firmware
// framebuffer before its driver gets another try. Two boots that died while
// it loaded may have been two power cuts rather than the driver; left there
// for good, the kiosk would wait for someone to know of the boot menu's way
// out.
const safeRetryEvery = 10

// markGood is MarkGood's change to the grubenv env, for a boot b on a machine
// whose display adapters are gpus; newLoader says whether this boot brought
// a GRUB script with new rules. It reports whether the kiosk should restart
// to give the GPU its driver.
func markGood(env Env, b Boot, gpus string, newLoader bool) (retry bool) {
	env["sc_kpending"] = "0"
	env["sc_gfxtry"] = ""
	if s, ok := env["sc_gfxsafe"]; ok {
		env["sc_gfxsafe"] = compactGPUs(s)
	}
	switch {
	case newLoader:
		retry = b.LeftSafe(gpus)
		env["sc_gfxsafe"] = ""
		env["sc_gfxfail"] = ""
		delete(env, "sc_gfxsafeboots")
	case b.LeftSafe(gpus):
		n, _ := strconv.Atoi(env["sc_gfxsafeboots"])
		if n+1 >= safeRetryEvery {
			// The next boot, whenever it comes, tries the driver: once.
			retryGPUs(env, gpus)
		} else {
			env["sc_gfxsafeboots"] = strconv.Itoa(n + 1)
		}
	}
	if b.Kernel == "new" || b.Kernel == "lts" {
		env["sc_kernel"] = b.Kernel
	}
	if b.Installed() {
		env["sc_boot"] = b.Slot
	}
	return retry
}

// retryGPUs takes the adapters gpus off the firmware framebuffer for the next
// boot, with a single try: they are already counted as having failed once
// (sc_gfxfail), so a boot that dies loading their driver puts them straight
// back rather than giving it another.
func retryGPUs(env Env, gpus string) {
	var keep []string
	for _, s := range strings.Split(env["sc_gfxsafe"], ",") {
		if s != "" && s != gpus {
			keep = append(keep, s)
		}
	}
	env["sc_gfxsafe"] = strings.Join(keep, ",")
	env["sc_gfxfail"] = gpus
	delete(env, "sc_gfxsafeboots")
}

// RetryGraphics has the next boot try the GPU's own driver again, once, on a
// boot left on the firmware framebuffer for want of it: the kiosk's way out
// (F4) of what two boots switched off just then would leave for good.
func RetryGraphics(b Boot) error {
	gpus := GPUs()
	if !b.LeftSafe(gpus) {
		return nil
	}
	return withEnv(b, func(env Env) { retryGPUs(env, gpus) })
}

// LeftSafe reports whether this boot is on the firmware framebuffer because
// the adapters gpus were remembered as taking a boot down — not because the
// boot menu asked for safe graphics this once, which a restart would undo.
func (b Boot) LeftSafe(gpus string) bool {
	if gpus == "" {
		return false
	}
	for _, s := range strings.Split(b.GfxSafe, ",") {
		if s == "once" {
			return false
		}
	}
	return b.SafeGraphics(gpus)
}

// newLoader returns this release's GRUB script when the one at path differs
// from it, and nil when they are the same or this release has none.
func newLoader(path string) []byte {
	want, err := os.ReadFile(loaderPath)
	if err != nil || len(want) == 0 {
		return nil
	}
	if have, err := os.ReadFile(path); err == nil && string(have) == string(want) {
		return nil
	}
	return want
}

// replaceFile writes data to path through a file beside it, so that a machine
// switched off halfway leaves either the old file or the new one.
func replaceFile(path string, data []byte) error {
	tmp := path + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
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
// followed by one of the slot it came from. See build/grub-disk.cfg.
func TryOther(b Boot) error {
	if !b.Installed() {
		return errNoEnv
	}
	return withEnv(b, func(env Env) { env["sc_try"] = b.Other() })
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
	return withMedium(b, func(dir, file string) error {
		return Modify(filepath.Join(dir, file), fn)
	})
}

// withMedium mounts the medium this boot came from and has fn work on it, given
// where it is mounted and the grubenv's path on it.
func withMedium(b Boot, fn func(dir, file string) error) error {
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
		return fn(dir, file)
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
