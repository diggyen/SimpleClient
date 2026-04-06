package scanner

import (
	"testing"
)

func TestExpandCIDR_24(t *testing.T) {
	ips, err := ExpandCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 254 {
		t.Fatalf("expected 254 IPs, got %d", len(ips))
	}
}

func TestExpandCIDR_30(t *testing.T) {
	ips, err := ExpandCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs, got %d", len(ips))
	}
}

func TestExpandCIDR_NoLoopback(t *testing.T) {
	ips, err := ExpandCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			t.Fatalf("loopback address %s should not be in results", ip)
		}
	}
}

func TestExpandCIDR_Invalid(t *testing.T) {
	_, err := ExpandCIDR("not-a-cidr")
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}
