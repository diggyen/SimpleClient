// Package wifi joins the kiosk to a wireless network through wpa_supplicant,
// which sc-net runs on every wireless interface: the networks in range, for
// the network dialog to list, and the one network the kiosk keeps and joins
// at every boot.
//
// The network is kept where the other settings are (see netconf): on the data
// partition of an installed kiosk, on the EFI partition of the USB stick a
// live one runs from. /init copies it to RunPath, the file wpa_supplicant
// reads. A WPA2 passphrase is kept as the key wpa_supplicant derives from it,
// an enterprise password as its NT hash; only WPA3-only networks, whose
// handshake needs the passphrase itself, keep it as typed.
package wifi

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4"

	"github.com/diggyen/SimpleClient/internal/bootenv"
)

// RunPath is the configuration wpa_supplicant runs with. /init writes it at
// boot from the kept network; sc-net starts wpa_supplicant with it.
var RunPath = "/run/sc-wifi.conf"

// dataPath is where an installed kiosk keeps the network; efiName is where a
// kiosk running from a USB stick keeps it, on the stick (bootenv.LiveDevice).
var (
	dataPath = "/data/simpleclient/wifi.conf"
	efiName  = "simpleclient/wifi.conf"
)

// ctrlDir is where wpa_supplicant listens for wpa_cli; sysNet lists the
// network interfaces; scNet starts wpa_supplicant (see build/sc-net). Tests
// point them elsewhere, and wpaCLI at a fake.
var (
	ctrlDir = "/run/wpa_supplicant"
	sysNet  = "/sys/class/net"
	wpaCLI  = "wpa_cli"
	scNet   = "/sbin/sc-net"
)

// Security is how a network lets clients in.
type Security int

const (
	// Open networks take anyone.
	Open Security = iota
	// PSK is WPA2 Personal, or WPA3 in transition mode, with a passphrase.
	PSK
	// SAE is WPA3 Personal only.
	SAE
	// EAP is WPA2/WPA3 Enterprise with a user name and a password (PEAP,
	// MSCHAPv2 inside), as Windows and Active Directory networks run it.
	EAP
	// WEP is long broken, and not offered.
	WEP
)

// Errors NewConfig returns for what was typed.
var (
	ErrName        = errors.New("wifi: a network name is 1 to 32 bytes")
	ErrPassphrase  = errors.New("wifi: a WPA passphrase is 8 to 63 characters")
	ErrIdentity    = errors.New("wifi: an enterprise network needs a user name")
	ErrUnsupported = errors.New("wifi: WEP networks are not supported")
)

// Network is one network in range.
type Network struct {
	SSID     string
	Signal   int // dBm, the strongest of its access points
	Security Security
}

// Bars grades the signal onto four bars, as the host list grades latency.
func (n Network) Bars() int {
	switch {
	case n.Signal >= -55:
		return 4
	case n.Signal >= -67:
		return 3
	case n.Signal >= -75:
		return 2
	}
	return 1
}

// Interfaces lists the wireless interfaces.
func Interfaces() []string {
	ents, _ := os.ReadDir(sysNet)
	var out []string
	for _, e := range ents {
		if IsWireless(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// IsWireless reports whether iface is a wireless interface.
func IsWireless(iface string) bool {
	_, err := os.Stat(filepath.Join(sysNet, iface, "wireless"))
	return err == nil
}

// cli runs wpa_cli against iface.
func cli(iface string, args ...string) (string, error) {
	out, err := exec.Command(wpaCLI, append([]string{"-p", ctrlDir, "-i", iface}, args...)...).Output()
	return string(out), err
}

// Scan asks wpa_supplicant on iface to look for networks; Results has them a
// few seconds later.
func Scan(iface string) error {
	out, err := cli(iface, "scan")
	if err != nil {
		return err
	}
	// FAIL-BUSY: a scan is under way already, which is as good.
	if s := strings.TrimSpace(out); s != "OK" && s != "FAIL-BUSY" {
		return errors.New("wifi: scan: " + s)
	}
	return nil
}

// Results is what the last scan on iface found: one entry per name, the
// strongest first. Hidden networks, which have none, are left out.
func Results(iface string) ([]Network, error) {
	out, err := cli(iface, "scan_results")
	if err != nil {
		return nil, err
	}
	return parseResults(out), nil
}

// parseResults reads wpa_cli's scan_results: a header, then one access point
// a line — BSSID, frequency, signal, flags, SSID — tab-separated.
func parseResults(out string) []Network {
	best := map[string]Network{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 5)
		if len(f) < 5 || strings.Count(f[0], ":") != 5 {
			continue
		}
		// A hidden network sends no name, or as many zero bytes as it has.
		ssid := unescape(f[4])
		if strings.Trim(ssid, "\x00") == "" {
			continue
		}
		sig, _ := strconv.Atoi(f[2])
		n := Network{SSID: ssid, Signal: sig, Security: security(f[3])}
		if o, ok := best[ssid]; !ok || n.Signal > o.Signal {
			best[ssid] = n
		}
	}
	out2 := make([]Network, 0, len(best))
	for _, n := range best {
		out2 = append(out2, n)
	}
	sort.Slice(out2, func(i, j int) bool {
		if out2[i].Signal != out2[j].Signal {
			return out2[i].Signal > out2[j].Signal
		}
		return out2[i].SSID < out2[j].SSID
	})
	return out2
}

// security reads the flags wpa_supplicant shows for an access point, such as
// [WPA2-PSK-CCMP][ESS] or [WPA2-PSK+SAE-CCMP][ESS] (WPA3 transition mode).
func security(flags string) Security {
	switch {
	case strings.Contains(flags, "EAP"):
		return EAP
	case strings.Contains(flags, "PSK"):
		return PSK
	case strings.Contains(flags, "SAE"):
		return SAE
	case strings.Contains(flags, "WEP"):
		return WEP
	}
	return Open
}

// unescape undoes the escaping wpa_supplicant prints SSIDs with: \\ \" \e
// \n \r \t, and \xNN for any other byte that is not printable.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b = append(b, s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'e':
			b = append(b, 0x1b)
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b = append(b, byte(v))
					i += 2
					continue
				}
			}
			b = append(b, 'x')
		default:
			b = append(b, s[i])
		}
	}
	return string(b)
}

// Config is the network the kiosk keeps.
type Config struct {
	SSID     string
	Security Security
	// Key is what joins it: for PSK the 256-bit key derived from the
	// passphrase, in hex; for SAE the passphrase; for EAP the password's NT
	// hash, in hex, which MSCHAPv2 needs instead of the password.
	Key      string
	Identity string // EAP only
}

// NewConfig is the network n, joined with what was typed for it.
func NewConfig(n Network, identity, passphrase string) (Config, error) {
	if n.SSID == "" || len(n.SSID) > 32 {
		return Config{}, ErrName
	}
	c := Config{SSID: n.SSID, Security: n.Security}
	switch n.Security {
	case PSK:
		if len(passphrase) < 8 || len(passphrase) > 63 {
			return Config{}, ErrPassphrase
		}
		c.Key = hex.EncodeToString(pbkdf2SHA1([]byte(passphrase), []byte(n.SSID), 4096, 32))
	case SAE:
		if passphrase == "" {
			return Config{}, ErrPassphrase
		}
		c.Key = passphrase
	case EAP:
		if identity == "" {
			return Config{}, ErrIdentity
		}
		c.Identity = identity
		c.Key = ntHash(passphrase)
	case WEP:
		return Config{}, ErrUnsupported
	}
	return c, nil
}

// pbkdf2SHA1 is PBKDF2 with HMAC-SHA1 (RFC 8018), which WPA uses to turn a
// passphrase and the network's name into its key.
func pbkdf2SHA1(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	var out []byte
	for block := 1; len(out) < keyLen; block++ {
		prf.Reset()
		prf.Write(salt)
		prf.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// ntHash is the MD4 of the password in UTF-16LE, in hex.
func ntHash(password string) string {
	h := md4.New()
	for _, u := range utf16.Encode([]rune(password)) {
		h.Write([]byte{byte(u), byte(u >> 8)})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Encode writes wpa_supplicant's configuration with c as its one network, or
// with none for the zero Config: wpa_supplicant runs either way, for the
// dialog to scan with. Every string is written in hex, which wpa_supplicant
// reads as well as a quoted one and which nothing typed can break out of.
func (c Config) Encode() []byte {
	var b strings.Builder
	b.WriteString("ctrl_interface=" + ctrlDir + "\nupdate_config=0\n")
	if c.SSID == "" {
		return []byte(b.String())
	}
	b.WriteString("network={\n\tssid=" + hex.EncodeToString([]byte(c.SSID)) + "\n\tscan_ssid=1\n")
	switch c.Security {
	case Open:
		b.WriteString("\tkey_mgmt=NONE\n")
	case PSK:
		b.WriteString("\tkey_mgmt=WPA-PSK\n\tpsk=" + c.Key + "\n")
	case SAE:
		b.WriteString("\tkey_mgmt=SAE\n\tieee80211w=2\n\tsae_password=" + hex.EncodeToString([]byte(c.Key)) + "\n")
	case EAP:
		b.WriteString("\tkey_mgmt=WPA-EAP\n\teap=PEAP\n\tidentity=" + hex.EncodeToString([]byte(c.Identity)) +
			"\n\tpassword=hash:" + c.Key + "\n\tphase2=\"auth=MSCHAPV2\"\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// Parse reads back what Encode wrote.
func Parse(b []byte) (Config, bool) {
	var c Config
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "network={":
			in = true
			continue
		case line == "}":
			in = false
			continue
		case !in:
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		unhex := func() string {
			d, err := hex.DecodeString(v)
			if err != nil {
				return ""
			}
			return string(d)
		}
		switch k {
		case "ssid":
			c.SSID = unhex()
		case "key_mgmt":
			switch v {
			case "WPA-PSK":
				c.Security = PSK
			case "SAE":
				c.Security = SAE
			case "WPA-EAP":
				c.Security = EAP
			}
		case "psk":
			c.Key = v
		case "sae_password":
			c.Key = unhex()
		case "identity":
			c.Identity = unhex()
		case "password":
			c.Key = strings.TrimPrefix(v, "hash:")
		}
	}
	return c, c.SSID != ""
}

// Load returns the network in effect.
func Load() (Config, bool) {
	b, err := os.ReadFile(RunPath)
	if err != nil {
		return Config{}, false
	}
	return Parse(b)
}

// Use puts c in effect for the next Apply without keeping it for the next
// boot: the dialog keeps a network only once the kiosk is on it.
func Use(c Config) error {
	return writeFile(RunPath, c.Encode())
}

// Apply has wpa_supplicant read RunPath again on every wireless interface,
// starting it where it is not running.
func Apply() error {
	out, err := exec.Command(scNet, "wifi").CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc-net wifi: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Save puts c in effect for the next Apply and keeps it for the next boot
// where it can, as netconf.Save does with the network settings; the zero
// Config forgets the network. persisted is false on a kiosk booted from a
// CD, where it lasts until the next restart.
func Save(c Config, installed bool) (persisted bool, err error) {
	b := c.Encode()
	if err := writeFile(RunPath, b); err != nil {
		return false, err
	}
	if installed {
		return writeFile(dataPath, b) == nil, nil
	}
	dev, err := bootenv.LiveDevice()
	if err != nil {
		return false, nil
	}
	err = bootenv.WithMounted(dev, "vfat", func(dir string) error {
		return writeFile(filepath.Join(dir, efiName), b)
	})
	return err == nil, nil
}

// writeFile replaces path whole, so a power cut mid-write leaves the old
// network rather than half of the new one. Only root reads it.
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

// Status is how iface stands with its network.
type Status struct {
	// State is wpa_supplicant's: COMPLETED once joined; SCANNING,
	// ASSOCIATING, 4WAY_HANDSHAKE on the way; DISCONNECTED, INACTIVE.
	State string
	SSID  string
	// Rejected is set when wpa_supplicant has given up on the network for a
	// while, which after a handshake means the passphrase was wrong.
	Rejected bool
}

// Joined reports whether iface is on its network.
func (s Status) Joined() bool { return s.State == "COMPLETED" }

// Busy reports whether iface is on its way onto its network: found, and
// being let in. Looking for a network that is not in range is not.
func (s Status) Busy() bool {
	switch s.State {
	case "AUTHENTICATING", "ASSOCIATING", "ASSOCIATED", "4WAY_HANDSHAKE", "GROUP_HANDSHAKE":
		return true
	}
	return false
}

// Current reports iface's status.
func Current(iface string) (Status, error) {
	out, err := cli(iface, "status")
	if err != nil {
		return Status{}, err
	}
	var s Status
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "wpa_state":
			s.State = v
		case "ssid":
			s.SSID = unescape(v)
		}
	}
	if out, err := cli(iface, "list_networks"); err == nil {
		s.Rejected = strings.Contains(out, "[TEMP-DISABLED]")
	}
	return s, nil
}
