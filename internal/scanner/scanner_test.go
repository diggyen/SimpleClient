package scanner

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
)

// startMockRDP binds a TCP listener on a random port of 127.0.0.1, which
// the tests then scan for.
func startMockRDP(t *testing.T) string {
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

func mustPort(t *testing.T, s string) int {
	t.Helper()
	p, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The scan goes to the port it is given, and the host it finds carries it:
// connecting to it must not fall back to 3389.
func TestScannerFindsHost(t *testing.T) {
	addr := startMockRDP(t)
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

	ch := s.Start(ctx, []string{cidr}, mustPort(t, port))

	found := false
	for ev := range ch {
		if ev.Type == domain.EventHostFound {
			found = true
			if got := ev.Host.AddrRDP(); got != addr {
				t.Errorf("found %s, want %s", got, addr)
			}
		}
	}
	if !found {
		t.Fatal("scanner should find the mock RDP host")
	}
}

func TestScannerCancel(t *testing.T) {
	s := New(8, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	ch := s.Start(ctx, []string{"10.255.0.0/24"}, 0)
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
	ch1 := s.Start(ctx, []string{"10.255.0.0/24"}, 0)

	// Start second scan — should cancel the first.
	addr := startMockRDP(t)
	host, port, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	ip3 := ip.To4()
	ip3[3] &= 0xFC
	cidr := ip3.String() + "/30"
	ch2 := s.Start(ctx, []string{cidr}, mustPort(t, port))

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

// A discovered host must always carry a latency the list can show. The scanner
// used to hardcode 0, which the UI reads as "not measured" and draws as an
// empty cell — so every host on a real network had a blank latency column.
func TestDialLatencyMs_NeverZeroForALiveHost(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int64
	}{
		{0, 1},                      // faster than the clock can resolve
		{120 * time.Microsecond, 1}, // a switched LAN
		{999 * time.Microsecond, 1},
		{time.Millisecond, 1},
		{1500 * time.Microsecond, 2}, // rounds up
		{47 * time.Millisecond, 47},
		{time.Second, 1000},
	}
	for _, c := range cases {
		if got := dialLatencyMs(c.in); got != c.want {
			t.Errorf("dialLatencyMs(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
