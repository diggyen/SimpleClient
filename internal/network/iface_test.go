package network

import (
	"net"
	"testing"
)

func TestDetectCIDR_ReturnsNonEmpty(t *testing.T) {
	cidr, err := DetectCIDR()
	// On CI or machines with no external network, this may fail — that's acceptable.
	if err != nil {
		t.Skipf("DetectCIDR: %v", err)
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("DetectCIDR returned invalid CIDR %q: %v", cidr, err)
	}
	if ipNet.IP.To4() == nil {
		t.Fatalf("expected IPv4 CIDR, got %q", cidr)
	}
	if ipNet.IP.IsLoopback() {
		t.Fatal("DetectCIDR should not return a loopback address")
	}
}

func TestDetectIP_ReturnsNonLoopback(t *testing.T) {
	ip, err := DetectIP()
	if err != nil {
		t.Skipf("DetectIP: %v", err)
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("DetectIP returned invalid IP %q", ip)
	}
	if parsed.IsLoopback() {
		t.Fatal("DetectIP should not return a loopback address")
	}
	if parsed.To4() == nil {
		t.Fatal("DetectIP should return an IPv4 address")
	}
}

func TestDetectCIDR_MatchesDetectIP(t *testing.T) {
	cidr, err1 := DetectCIDR()
	ip, err2 := DetectIP()
	if err1 != nil || err2 != nil {
		t.Skip("network detection unavailable")
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	if !ipNet.Contains(net.ParseIP(ip)) {
		t.Fatalf("CIDR %q does not contain IP %q", cidr, ip)
	}
}
