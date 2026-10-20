package netconf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromForm(t *testing.T) {
	c, err := FromForm(true, "", " 192.168.1.50 ", "255.255.255.0", "192.168.1.1", "192.168.1.1", " 1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if one, err := FromForm(true, "", "192.168.1.50", "", "", "192.168.1.1, 1.1.1.1", ""); err != nil || len(one.DNS) != 2 {
		t.Fatalf("two servers in one field, comma-separated: %+v %v", one, err)
	}
	if c.Address != "192.168.1.50/24" || c.Gateway != "192.168.1.1" || len(c.DNS) != 2 || c.DNS[1] != "1.1.1.1" {
		t.Fatalf("got %+v", c)
	}
	if want := "mode=static\ninterface=\naddress=192.168.1.50/24\ngateway=192.168.1.1\ndns=192.168.1.1 1.1.1.1\n"; string(c.Encode()) != want {
		t.Fatalf("encoded %q", c.Encode())
	}
	back := Parse(c.Encode())
	if !back.Static || back.Address != c.Address || back.Gateway != c.Gateway || len(back.DNS) != 2 {
		t.Fatalf("round trip gave %+v", back)
	}
	ip, mask, gw, dns1, dns2 := back.Form()
	if ip != "192.168.1.50" || mask != "255.255.255.0" || gw != "192.168.1.1" || dns1 != "192.168.1.1" || dns2 != "1.1.1.1" {
		t.Fatalf("form %q %q %q %q %q", ip, mask, gw, dns1, dns2)
	}

	for _, m := range []string{"", "24", "/24"} {
		c, err := FromForm(true, "", "10.0.0.7", m, "", "")
		if err != nil || c.Address != "10.0.0.7/24" {
			t.Errorf("mask %q: %+v %v", m, c, err)
		}
	}
	if c, err := FromForm(true, "", "10.0.0.7", "255.255.0.0", "10.0.200.1", ""); err != nil || c.Address != "10.0.0.7/16" {
		t.Errorf("/16: %+v %v", c, err)
	}

	for _, bad := range []struct {
		ip, mask, gw, dns string
		want              error
	}{
		{"", "", "", "", ErrAddress},
		{"192.168.1", "", "", "", ErrAddress},
		{"0.0.0.0", "", "", "", ErrAddress},
		{"192.168.1.50", "255.0.255.0", "", "", ErrMask},
		{"192.168.1.50", "33", "", "", ErrMask},
		{"192.168.1.50", "", "192.168.2.1", "", ErrGateway},
		{"192.168.1.50", "", "192.168.1.50", "", ErrGateway},
		{"192.168.1.50", "", "", "dns.example", ErrDNS},
	} {
		if _, err := FromForm(true, "", bad.ip, bad.mask, bad.gw, bad.dns); err != bad.want {
			t.Errorf("%+v: got %v, want %v", bad, err, bad.want)
		}
	}

	if c, err := FromForm(false, "", "garbage", "", "", ""); err != nil || c.Static {
		t.Errorf("DHCP ignores the static fields: %+v %v", c, err)
	}
}

// The port rides along with either mode, and RDP's own is written as nothing.
func TestPort(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
		err  bool
	}{
		{"", 0, false}, {" 3389 ", 0, false}, {"3390", 3390, false}, {"65535", 65535, false},
		{"0", 0, true}, {"65536", 0, true}, {"33a", 0, true}, {"-1", 0, true},
	} {
		got, err := ParsePort(c.in)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("ParsePort(%q) = %d, %v", c.in, got, err)
		}
	}
	for _, c := range []Config{
		{Port: 3390},
		{Static: true, Interface: "eth0", Address: "10.0.0.7/24", DNS: []string{"10.0.0.1"}, Port: 53389},
	} {
		if got := Parse(c.Encode()); got.Port != c.Port || got.Static != c.Static {
			t.Errorf("%+v came back as %+v from %q", c, got, c.Encode())
		}
	}
	if b := (Config{}).Encode(); string(b) != "mode=dhcp\n" {
		t.Errorf("DHCP on 3389 encodes as %q", b)
	}
}

func TestParse_DefaultsToDHCP(t *testing.T) {
	for _, in := range []string{"", "mode=dhcp\n", "mode=static\n", "garbage"} {
		if c := Parse([]byte(in)); c.Static {
			t.Errorf("%q parsed as static", in)
		}
	}
}

// Save puts the settings in effect and, on an installed kiosk, on the data
// partition.
func TestSave_RunPath(t *testing.T) {
	dir := t.TempDir()
	old := RunPath
	RunPath = filepath.Join(dir, "sc-network")
	t.Cleanup(func() { RunPath = old })

	c, _ := FromForm(true, "eth0", "10.0.0.7", "24", "10.0.0.1", "")
	if _, err := Save(c, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(RunPath)
	if err != nil || string(b) != string(c.Encode()) {
		t.Fatalf("run file %q %v", b, err)
	}
	if got := Load(); got.Address != "10.0.0.7/24" || got.Interface != "eth0" {
		t.Fatalf("Load gave %+v", got)
	}
}
