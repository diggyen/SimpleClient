package scanner

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
)

// startMockRDP3389 binds a TCP listener on 127.0.0.1:3389 if available.
// Falls back to a random port and patches the scanner to use that port.
func startMockRDP3389(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start mock RDP: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func TestScannerFindsHost(t *testing.T) {
	addr := startMockRDP3389(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	// Use /30 to get 2 usable IPs — one of which is our mock server.
	s := New(16, 500*time.Millisecond)
	ctx := context.Background()

	// Derive a /30 from the mock host.
	ip := net.ParseIP(host)
	ip3 := ip.To4()
	ip3[3] &= 0xFC // zero last 2 bits for /30 network
	cidr := ip3.String() + "/30"

	ch := s.startWithPort(ctx, []string{cidr}, port)

	found := false
	for ev := range ch {
		if ev.Type == domain.EventHostFound {
			found = true
		}
	}
	if !found {
		t.Fatal("scanner should find the mock RDP host")
	}
}

func TestScannerCancel(t *testing.T) {
	s := New(8, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	ch := s.Start(ctx, []string{"10.255.0.0/24"})
	// Cancel almost immediately.
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Drain channel; it should close within a reasonable time.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scanner did not stop after cancel")
	}
}

func TestScannerDoubleStart(t *testing.T) {
	s := New(16, 200*time.Millisecond)

	// Start first scan on a large, unreachable subnet.
	ctx := context.Background()
	ch1 := s.Start(ctx, []string{"10.255.0.0/24"})

	// Start second scan — should cancel the first.
	addr := startMockRDP3389(t)
	host, port, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	ip3 := ip.To4()
	ip3[3] &= 0xFC
	cidr := ip3.String() + "/30"
	ch2 := s.startWithPort(ctx, []string{cidr}, port)

	// First channel should close.
	drained := false
	timeout := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch1:
			if !ok {
				drained = true
			}
			if drained {
				goto checkCh2
			}
		case <-timeout:
			t.Fatal("first scan channel did not close after double Start()")
		}
	}
checkCh2:

	// Second scan should find the host.
	found := false
	for ev := range ch2 {
		if ev.Type == domain.EventHostFound {
			found = true
		}
	}
	if !found {
		t.Fatal("second scan should find the mock RDP host")
	}
}

func TestExpandCIDRCounts(t *testing.T) {
	ips, err := ExpandCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 254 {
		t.Fatalf("expected 254 IPs for /24, got %d", len(ips))
	}
}
