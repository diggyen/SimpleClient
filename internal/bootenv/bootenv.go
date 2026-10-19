// Package bootenv is the state the boot loader and the running kiosk share.
//
// GRUB keeps it in its environment block, a grubenv file on the boot medium:
// which kernel and which system slot to boot, and whether the last boot came
// up. GRUB marks every boot pending before it starts the kernel; the kiosk
// clears the mark once it is running, and records what it runs on. A mark
// still set at the next boot means the last one never got that far, and GRUB
// tries the other kernel — see build/grub.cfg and build/grub-disk.cfg.
package bootenv

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	env, err := Parse(old)
	if err != nil {
		return err
	}
	changed := false
	for k, v := range set {
		if cur, ok := env[k]; !ok || cur != v {
			env[k] = v
			changed = true
		}
	}
	if !changed {
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
}

// Installed reports whether the kiosk runs from a disk install.
func (b Boot) Installed() bool { return b.Slot != "" }

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
	}
	return b
}

// MarkGood records that this boot came up: the kernel it runs on is the one
// to keep using and, on a disk install, so is the slot — which is what makes
// a freshly installed update permanent. The grubenv is on the ESP of a disk
// install and on the SC_EFI partition of a USB stick; booted from a CD there
// is nothing to write, and nothing is.
func MarkGood(b Boot) error {
	label, file := "SC_EFI", "grubenv"
	set := map[string]string{"sc_kpending": "0"}
	if b.Kernel == "new" || b.Kernel == "lts" {
		set["sc_kernel"] = b.Kernel
	}
	if b.Installed() {
		label, file = "SC_ESP", "boot/grub/grubenv"
		set["sc_boot"] = b.Slot
	}
	dev, err := FindLabel(label)
	if err != nil {
		return nil
	}
	return WithMounted(dev, "vfat", func(dir string) error {
		return Update(filepath.Join(dir, file), set)
	})
}

// FindLabel returns the device holding the file system labelled label.
func FindLabel(label string) (string, error) {
	out, err := exec.Command("findfs", "LABEL="+label).Output()
	dev := strings.TrimSpace(string(out))
	if err != nil || dev == "" {
		return "", fmt.Errorf("bootenv: no file system labelled %s", label)
	}
	return dev, nil
}
