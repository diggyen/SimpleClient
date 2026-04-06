package domain

import (
	"net"
	"testing"
)

func TestHostDisplayName_WithHostname(t *testing.T) {
	h := Host{IP: net.ParseIP("192.168.1.10"), Hostname: "server01"}
	if got := h.DisplayName(); got != "server01" {
		t.Fatalf("expected 'server01', got %q", got)
	}
}

func TestHostDisplayName_WithoutHostname(t *testing.T) {
	h := Host{IP: net.ParseIP("192.168.1.10")}
	if got := h.DisplayName(); got != "192.168.1.10" {
		t.Fatalf("expected '192.168.1.10', got %q", got)
	}
}

func TestHostAddrRDP(t *testing.T) {
	h := Host{IP: net.ParseIP("10.0.0.5")}
	if got := h.AddrRDP(); got != "10.0.0.5:3389" {
		t.Fatalf("expected '10.0.0.5:3389', got %q", got)
	}
}
