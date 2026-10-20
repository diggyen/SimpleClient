package scanner

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
)

// NetworkScanner implements domain.Scanner by probing the RDP port — 3389
// unless the network settings say otherwise — concurrently.
type NetworkScanner struct {
	concurrency int
	timeout     time.Duration
	mu          sync.RWMutex
	hosts       map[string]domain.Host
	cancel      context.CancelFunc
}

// New creates a scanner with the given worker count and per-host dial timeout.
func New(concurrency int, timeout time.Duration) *NetworkScanner {
	return &NetworkScanner{
		concurrency: concurrency,
		timeout:     timeout,
		hosts:       make(map[string]domain.Host),
	}
}

// Start expands the given CIDRs into individual IPs and probes each for
// port, 0 being domain.DefaultRDPPort. Scan events are streamed on the
// returned channel, which is closed when the scan completes or the context is
// cancelled. The hosts found carry the port when it is not the default, so a
// connection goes where the scan found them.
func (s *NetworkScanner) Start(ctx context.Context, cidrs []string, port int) <-chan domain.ScanEvent {
	// A host on the default port says 0, as typed and remembered ones do.
	if port == domain.DefaultRDPPort {
		port = 0
	}

	// Cancel any previously running scan.
	if s.cancel != nil {
		s.cancel()
	}

	scanCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Collect all IPs to probe.
	var ips []net.IP
	for _, cidr := range cidrs {
		expanded, err := ExpandCIDR(cidr)
		if err == nil {
			ips = append(ips, expanded...)
		}
	}

	ch := make(chan domain.ScanEvent, 64)

	go func() {
		defer close(ch)
		if len(ips) == 0 {
			ch <- domain.ScanEvent{Type: domain.EventScanComplete, Total: 0}
			return
		}

		start := time.Now()
		total := len(ips)
		sem := make(chan struct{}, s.concurrency)
		var wg sync.WaitGroup
		var mu sync.Mutex
		scanned := 0

	scanLoop:
		for _, ip := range ips {
			// Check cancellation before dispatching each worker.
			select {
			case <-scanCtx.Done():
				break scanLoop
			default:
			}

			// Acquire semaphore slot (blocks if at max concurrency).
			select {
			case sem <- struct{}{}:
			case <-scanCtx.Done():
				break scanLoop
			}

			wg.Add(1)
			go func(ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()

				addr := domain.Host{IP: ip, Port: port}.AddrRDP()
				start := time.Now()
				conn, err := net.DialTimeout("tcp", addr, s.timeout)
				if err == nil {
					// Time the TCP handshake, and do it before the reverse
					// lookup so a slow DNS server is not charged to the host.
					latency := dialLatencyMs(time.Since(start))
					hostname := reverseLookup(ip)
					if hostname == "" {
						hostname = rdpName(conn)
					}
					conn.Close()
					host := domain.Host{
						IP:           ip,
						Hostname:     hostname,
						Port:         port,
						LatencyMs:    latency,
						DiscoveredAt: time.Now(),
					}
					s.mu.Lock()
					s.hosts[ip.String()] = host
					s.mu.Unlock()

					select {
					case ch <- domain.ScanEvent{Type: domain.EventHostFound, Host: &host}:
					case <-scanCtx.Done():
					}
				}

				mu.Lock()
				scanned++
				current := scanned
				mu.Unlock()

				if current%10 == 0 {
					select {
					case ch <- domain.ScanEvent{
						Type:    domain.EventScanProgress,
						Scanned: current,
						Total:   total,
					}:
					default:
					}
				}
			}(ip)
		}

		wg.Wait()
		ch <- domain.ScanEvent{
			Type:       domain.EventScanComplete,
			Scanned:    total,
			Total:      total,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}()

	return ch
}

// Cancel stops an in-progress scan.
func (s *NetworkScanner) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Hosts returns a snapshot of discovered hosts.
func (s *NetworkScanner) Hosts() []domain.Host {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Host, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, h)
	}
	return out
}

// dialLatencyMs converts a measured handshake time to the whole milliseconds
// the UI displays, rounding up.
//
// It never returns zero for a host that answered. A switched LAN answers in
// well under a millisecond, and truncating that to 0 is indistinguishable from
// "not measured" — which is what the list treats it as, so a whole column of
// perfectly good hosts would render blank.
func dialLatencyMs(d time.Duration) int64 {
	if ms := (d.Microseconds() + 999) / 1000; ms > 0 {
		return ms
	}
	return 1
}

// reverseLookup performs a reverse DNS lookup with a 200ms timeout.
func reverseLookup(ip net.IP) string {
	resolver := &net.Resolver{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	names, err := resolver.LookupAddr(ctx, ip.String())
	if err != nil || len(names) == 0 {
		return ""
	}
	name := names[0]
	// Strip trailing dot from FQDN.
	if len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	return name
}
