package domain

import "context"

// Scanner is implemented by any component that can discover RDP hosts.
type Scanner interface {
	// Start begins scanning the given CIDR ranges and streams events on the
	// returned channel. The channel is closed when scanning completes or the
	// context is cancelled.
	Start(ctx context.Context, cidrs []string) <-chan ScanEvent

	// Cancel stops an in-progress scan immediately.
	Cancel()

	// Hosts returns a snapshot of all hosts found so far.
	Hosts() []Host
}
