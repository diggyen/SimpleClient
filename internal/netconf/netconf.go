// Package netconf holds the kiosk's network settings — DHCP, or a fixed
// address with its gateway and DNS servers — and applies them through
// /sbin/sc-net, the script /init runs at boot, so a setting behaves the same
// whether it was just changed or the kiosk has just started.
package netconf

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/diggyen/SimpleClient/internal/bootenv"
)

// RunPath holds the settings in effect; /init writes it at boot.
var RunPath = "/run/sc-network"

// dataPath is where an installed kiosk keeps its settings; efiName is where a
// kiosk running from a USB stick keeps them, on the stick (bootenv.LiveDevice).
const (
	dataPath = "/data/simpleclient/network"
	efiName  = "simpleclient/network"
)

// Config is one set of network settings.
type Config struct {
	Static    bool
	Interface string // empty: the first interface
	Address   string // with its prefix length: 192.168.1.50/24
	Gateway   string // may be empty
	DNS       []string
}

// Parse reads the key=value form sc-net reads. Anything it does not know, or a
// missing file, means DHCP.
func Parse(b []byte) Config {
	var c Config
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		switch k {
		case "mode":
			c.Static = v == "static"
		case "interface":
			c.Interface = v
		case "address":
			c.Address = v
		case "gateway":
			c.Gateway = v
		case "dns":
			c.DNS = strings.Fields(v)
		}
	}
	if c.Address == "" {
		c.Static = false
	}
	return c
}

// Encode writes c the way Parse and sc-net read it.
func (c Config) Encode() []byte {
	if !c.Static {
		return []byte("mode=dhcp\n")
	}
	return []byte(fmt.Sprintf("mode=static\ninterface=%s\naddress=%s\ngateway=%s\ndns=%s\n",
		c.Interface, c.Address, c.Gateway, strings.Join(c.DNS, " ")))
}

// Load returns the settings in effect.
func Load() Config {
	b, _ := os.ReadFile(RunPath)
	return Parse(b)
}

// Errors from FromForm, one per field, so the dialog can say which is wrong.
var (
	ErrAddress = errors.New("netconf: bad address")
	ErrMask    = errors.New("netconf: bad subnet mask")
	ErrGateway = errors.New("netconf: gateway outside the subnet")
	ErrDNS     = errors.New("netconf: bad DNS server")
)

// FromForm builds settings from the dialog's fields. The mask may be written
// either way people write it — 255.255.255.0 or 24 — and defaults to /24;
// the gateway and the DNS servers, one per field, are optional. A field may
// still hold several servers separated by commas or spaces.
func FromForm(static bool, iface, ip, mask, gw string, dns ...string) (Config, error) {
	c := Config{Static: static, Interface: iface}
	if !static {
		return c, nil
	}
	addr := net.ParseIP(strings.TrimSpace(ip)).To4()
	if addr == nil || addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() {
		return c, ErrAddress
	}
	prefix, err := parseMask(mask)
	if err != nil {
		return c, err
	}
	c.Address = fmt.Sprintf("%s/%d", addr, prefix)

	if gw = strings.TrimSpace(gw); gw != "" {
		g := net.ParseIP(gw).To4()
		subnet := &net.IPNet{IP: addr.Mask(net.CIDRMask(prefix, 32)), Mask: net.CIDRMask(prefix, 32)}
		if g == nil || !subnet.Contains(g) || g.Equal(addr) {
			return c, ErrGateway
		}
		c.Gateway = g.String()
	}
	sep := func(r rune) bool { return r == ' ' || r == ',' || r == ';' }
	for _, field := range dns {
		for _, d := range strings.FieldsFunc(field, sep) {
			ip := net.ParseIP(d)
			if ip == nil {
				return c, ErrDNS
			}
			c.DNS = append(c.DNS, ip.String())
		}
	}
	return c, nil
}

func parseMask(s string) (int, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "/")
	if s == "" {
		return 24, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 || n > 32 {
			return 0, ErrMask
		}
		return n, nil
	}
	m := net.ParseIP(s).To4()
	if m == nil {
		return 0, ErrMask
	}
	ones, bits := net.IPMask(m).Size()
	if bits == 0 || ones == 0 {
		return 0, ErrMask // not contiguous, or 0.0.0.0
	}
	return ones, nil
}

// Form gives the dialog's fields for c: address, mask (dotted), gateway, and
// the preferred and alternate DNS servers.
func (c Config) Form() (ip, mask, gw, dns1, dns2 string) {
	if len(c.DNS) > 0 {
		dns1 = c.DNS[0]
	}
	if len(c.DNS) > 1 {
		dns2 = c.DNS[1]
	}
	addr, n, err := net.ParseCIDR(c.Address)
	if err != nil {
		return "", "255.255.255.0", c.Gateway, dns1, dns2
	}
	return addr.String(), net.IP(n.Mask).String(), c.Gateway, dns1, dns2
}

// Save puts c in effect for the next sc-net run and keeps it for the next boot
// where it can: the data partition of an installed kiosk, else the EFI
// partition of the USB stick the kiosk runs from. A kiosk booted from a CD has
// nowhere to keep it; persisted then reports false, and the settings last
// until the next restart.
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
// settings rather than half of the new ones.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
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

// Apply brings the network up as RunPath says, now.
func Apply() error {
	out, err := exec.Command("/sbin/sc-net", "apply").CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc-net: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Interfaces lists the network interfaces, loopback aside, in the order sc-net
// picks from.
func Interfaces() []string {
	ents, _ := os.ReadDir("/sys/class/net")
	var out []string
	for _, e := range ents {
		if e.Name() != "lo" {
			out = append(out, e.Name())
		}
	}
	return out
}

// Lease is what iface is running with now — address, default gateway, DNS
// servers — written as settings, whether it came from DHCP or not.
func Lease(iface string) Config {
	c := Config{Interface: iface, Address: Current(iface).Address}
	c.Gateway = defaultGateway(iface)
	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			if f := strings.Fields(sc.Text()); len(f) == 2 && f[0] == "nameserver" {
				c.DNS = append(c.DNS, f[1])
			}
		}
	}
	return c
}

// defaultGateway reads iface's default route from /proc/net/route, where the
// gateway is a little-endian hex word.
func defaultGateway(iface string) string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[0] != iface || f[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
	}
	return ""
}

// Status is what an interface has now: its IPv4 address and whether a cable is
// in, for the dialog to show under the settings.
type Status struct {
	Interface string
	Address   string // empty when it has none
	Carrier   bool
}

// Current reports the status of iface, or of the first interface.
func Current(iface string) Status {
	if iface == "" {
		if all := Interfaces(); len(all) > 0 {
			iface = all[0]
		}
	}
	s := Status{Interface: iface}
	if b, err := os.ReadFile("/sys/class/net/" + iface + "/carrier"); err == nil {
		s.Carrier = strings.TrimSpace(string(b)) == "1"
	}
	if ifi, err := net.InterfaceByName(iface); err == nil {
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				s.Address = n.String()
				break
			}
		}
	}
	return s
}
