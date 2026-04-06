package domain

import (
	"net"
	"time"
)

// Host represents a discovered RDP-capable host on the network.
type Host struct {
	IP           net.IP
	Hostname     string
	LatencyMs    int64
	DiscoveredAt time.Time
}

// AddrRDP returns the host's RDP endpoint address (IP:3389).
func (h Host) AddrRDP() string {
	return h.IP.String() + ":3389"
}

// DisplayName returns the hostname if available, otherwise the IP string.
func (h Host) DisplayName() string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return h.IP.String()
}
