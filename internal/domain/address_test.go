package domain

import (
	"net"
	"testing"
)

func TestParseIPAddress(t *testing.T) {
	for _, c := range []struct {
		in   string
		ip   string
		port int
		rdp  string
		addr string
	}{
		{"10.0.0.5", "10.0.0.5", 0, "10.0.0.5:3389", "10.0.0.5"},
		{" 10.0.0.5 ", "10.0.0.5", 0, "10.0.0.5:3389", "10.0.0.5"},
		{"10.0.0.5:3390", "10.0.0.5", 3390, "10.0.0.5:3390", "10.0.0.5:3390"},
		{"10.0.0.5:3389", "10.0.0.5", 3389, "10.0.0.5:3389", "10.0.0.5"},
		{"[fd00::5]:3390", "fd00::5", 3390, "[fd00::5]:3390", "[fd00::5]:3390"},
		{"fd00::5", "fd00::5", 0, "[fd00::5]:3389", "fd00::5"},
	} {
		h, err := ParseIPAddress(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !h.IP.Equal(net.ParseIP(c.ip)) || h.Port != c.port {
			t.Errorf("%q: got %s port %d, want %s port %d", c.in, h.IP, h.Port, c.ip, c.port)
		}
		if h.AddrRDP() != c.rdp || h.Address() != c.addr {
			t.Errorf("%q: AddrRDP %q Address %q, want %q %q", c.in, h.AddrRDP(), h.Address(), c.rdp, c.addr)
		}
	}
	for _, bad := range []string{"", "  ", "10.0.0.5:0", "10.0.0.5:70000", "10.0.0.5:x",
		"a b", "user@host", "server", "10.0.0.5/24"} {
		if h, err := ParseIPAddress(bad); err == nil {
			t.Errorf("%q parsed as %v, want an error", bad, h)
		}
	}
}

// A name goes through the resolver; localhost resolves without a network.
func TestParseAddress_Name(t *testing.T) {
	h, err := ParseAddress("localhost:3390")
	if err != nil {
		t.Skipf("no resolver: %v", err)
	}
	if !h.IP.IsLoopback() || h.Port != 3390 || h.Hostname != "localhost" {
		t.Fatalf("got %+v", h)
	}
	if h.DisplayName() != "localhost" {
		t.Fatalf("DisplayName = %q", h.DisplayName())
	}
}

func TestHostSame(t *testing.T) {
	a := Host{IP: net.ParseIP("10.0.0.5")}
	if !a.Same(Host{IP: net.ParseIP("10.0.0.5"), Port: 3389}) {
		t.Error("port 0 and 3389 are the same endpoint")
	}
	if a.Same(Host{IP: net.ParseIP("10.0.0.5"), Port: 3390}) {
		t.Error("different ports are different endpoints")
	}
}
