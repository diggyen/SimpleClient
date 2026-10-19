// Package autoconnect keeps the server a kiosk goes to as it starts, chosen
// with F10 in the connection dialog: either the dialog opens for that server
// with the account filled in, or the kiosk logs on to it by itself — at
// startup and again after every session, the way a thin client on a counter
// or in a classroom is expected to.
//
// It is kept where the network settings are (see netconf): on the data
// partition of an installed kiosk, on the EFI partition of the USB stick a
// live one runs from. /init copies it to RunPath at boot. A kiosk booted from
// a CD has nowhere to keep it, and keeps it until the next restart.
package autoconnect

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/diggyen/SimpleClient/internal/bootenv"
)

// RunPath holds the setting in effect; /init writes it at boot.
var RunPath = "/run/sc-autoconnect"

// dataPath is where an installed kiosk keeps the setting; efiName is where a
// kiosk running from a USB stick keeps it, on the stick's EFI partition.
var (
	dataPath = "/data/simpleclient/autoconnect"
	efiLabel = "SC_EFI"
	efiName  = "simpleclient/autoconnect"
)

// Mode is what the kiosk does with the server as it starts.
type Mode int

const (
	// Off lists the servers, as a kiosk without the setting does.
	Off Mode = iota
	// Dialog opens the connection dialog for the server, the account filled
	// in and the cursor in the password.
	Dialog
	// Logon logs on to the server with the password kept here.
	Logon
)

// Next is the mode F10 moves to.
func (m Mode) Next() Mode { return (m + 1) % 3 }

// Config is the server and the account to go to it with.
type Config struct {
	Mode     Mode
	Server   string // as typed: an address or a name, with an optional port
	Username string
	Domain   string
	// Password is kept for Logon only. The file holds it base64-encoded so
	// that it does not show to whoever opens the file; that is not
	// encryption, and the dialog says the password is kept on the kiosk.
	Password string
}

// Parse reads the key=value form Encode writes. Anything incomplete is no
// setting at all.
func Parse(b []byte) (Config, bool) {
	var c Config
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "=")
		if !ok {
			continue
		}
		switch k {
		case "mode":
			switch v {
			case "dialog":
				c.Mode = Dialog
			case "logon":
				c.Mode = Logon
			}
		case "server":
			c.Server = strings.TrimSpace(v)
		case "username":
			c.Username = v
		case "domain":
			c.Domain = v
		case "password":
			p, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return Config{}, false
			}
			c.Password = string(p)
		}
	}
	if c.Mode == Off || c.Server == "" || (c.Mode == Logon && c.Username == "") {
		return Config{}, false
	}
	if c.Mode != Logon {
		c.Password = ""
	}
	return c, true
}

// Encode writes c the way Parse reads it.
func (c Config) Encode() []byte {
	mode := "dialog"
	password := ""
	if c.Mode == Logon {
		mode = "logon"
		password = base64.StdEncoding.EncodeToString([]byte(c.Password))
	}
	return []byte(fmt.Sprintf("mode=%s\nserver=%s\nusername=%s\ndomain=%s\npassword=%s\n",
		mode, oneLine(c.Server), oneLine(c.Username), oneLine(c.Domain), password))
}

// oneLine keeps a typed value from breaking the file into more lines.
func oneLine(s string) string {
	return strings.NewReplacer("\n", "", "\r", "").Replace(s)
}

// Load returns the setting in effect.
func Load() (Config, bool) {
	b, err := os.ReadFile(RunPath)
	if err != nil {
		return Config{}, false
	}
	return Parse(b)
}

// Save puts c in effect and keeps it for the next boot where it can, as
// netconf.Save does with the network settings; a Config whose Mode is Off
// removes the setting. persisted is false on a kiosk booted from a CD, where
// the setting lasts until the next restart.
func Save(c Config, installed bool) (persisted bool, err error) {
	if c.Mode == Off {
		if err := remove(RunPath); err != nil {
			return false, err
		}
	} else if err := writeFile(RunPath, c.Encode()); err != nil {
		return false, err
	}
	keep := func(path string) error {
		if c.Mode == Off {
			return remove(path)
		}
		return writeFile(path, c.Encode())
	}
	if installed {
		return keep(dataPath) == nil, nil
	}
	dev, err := bootenv.FindLabel(efiLabel)
	if err != nil {
		return false, nil
	}
	err = bootenv.WithMounted(dev, "vfat", func(dir string) error {
		return keep(filepath.Join(dir, efiName))
	})
	return err == nil, nil
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// writeFile replaces path whole, so a power cut mid-write leaves the old
// setting rather than half of the new one.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
