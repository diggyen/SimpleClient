package scanner

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/kullanici/rdpboot/internal/domain"
)

// NetworkScanner implements domain.Scanner by probing port 3389 concurrently.
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
// port 3389. Scan events are streamed on the returned channel, which is
// closed when the scan completes or the context is cancelled.
func (s *NetworkScanner) Start(ctx context.Context, cidrs []string) <-chan domain.ScanEvent {
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

				addr := ip.String() + ":3389"
				conn, err := net.DialTimeout("tcp", addr, s.timeout)
				if err == nil {
					conn.Close()
					hostname := reverseLookup(ip)
					host := domain.Host{
						IP:           ip,
						Hostname:     hostname,
						LatencyMs:    0,
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

// startWithPort is like Start but dials a custom port instead of 3389.
// This exists for testing so mock listeners on random ports can be discovered.
func (s *NetworkScanner) startWithPort(ctx context.Context, cidrs []string, port string) <-chan domain.ScanEvent {
	if s.cancel != nil {
		s.cancel()
	}
	scanCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.mu.Lock()
	s.hosts = make(map[string]domain.Host)
	s.mu.Unlock()

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

		total := len(ips)
		sem := make(chan struct{}, s.concurrency)
		var wg sync.WaitGroup

		for _, ip := range ips {
			select {
			case <-scanCtx.Done():
				wg.Wait()
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()

				addr := ip.String() + ":" + port
				conn, err := net.DialTimeout("tcp", addr, s.timeout)
				if err == nil {
					conn.Close()
					host := domain.Host{IP: ip, DiscoveredAt: time.Now()}
					s.mu.Lock()
					s.hosts[ip.String()] = host
					s.mu.Unlock()
					select {
					case ch <- domain.ScanEvent{Type: domain.EventHostFound, Host: &host}:
					case <-scanCtx.Done():
					}
				}
			}(ip)
		}
		wg.Wait()
		ch <- domain.ScanEvent{Type: domain.EventScanComplete, Scanned: total, Total: total}
	}()

	return ch
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
