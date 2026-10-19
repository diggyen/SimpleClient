package domain

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultRDPPort is where RDP listens unless told otherwise.
const DefaultRDPPort = 3389

// Host represents a discovered RDP-capable host on the network.
type Host struct {
	IP           net.IP
	Hostname     string
	Port         int // 0 means DefaultRDPPort
	LatencyMs    int64
	DiscoveredAt time.Time
}

// AddrRDP returns the host's RDP endpoint address (IP:port).
func (h Host) AddrRDP() string {
	return net.JoinHostPort(h.IP.String(), strconv.Itoa(h.port()))
}

// Address is the host as a person would type it: the IP, with the port only
// when it is not the default.
func (h Host) Address() string {
	if h.port() == DefaultRDPPort {
		return h.IP.String()
	}
	return h.AddrRDP()
}

func (h Host) port() int {
	if h.Port == 0 {
		return DefaultRDPPort
	}
	return h.Port
}

// Same reports whether h and o are the same RDP endpoint.
func (h Host) Same(o Host) bool {
	return h.IP.Equal(o.IP) && h.port() == o.port()
}

// DisplayName returns the hostname if available, otherwise the IP string.
func (h Host) DisplayName() string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return h.IP.String()
}

// ErrBadAddress is returned by ParseAddress for input that is not an address.
var ErrBadAddress = errors.New("not a host address")

// resolveTimeout bounds the DNS lookup of a typed name: a kiosk whose DHCP
// handed out no working DNS server should say so rather than hang.
const resolveTimeout = 4 * time.Second

// splitAddress separates an optional :port from a typed address.
func splitAddress(s string) (name string, port int, err error) {
	s = strings.TrimSpace(s)
	name = s
	if h, p, e := net.SplitHostPort(s); e == nil {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return "", 0, ErrBadAddress
		}
		name, port = h, n
	}
	name = strings.Trim(name, "[]")
	if name == "" || strings.ContainsAny(name, " /\\@") {
		return "", 0, ErrBadAddress
	}
	return name, port, nil
}

// ParseIPAddress parses an IP address with an optional :port, without
// looking anything up.
func ParseIPAddress(s string) (Host, error) {
	name, port, err := splitAddress(s)
	if err != nil {
		return Host{}, err
	}
	ip := net.ParseIP(name)
	if ip == nil {
		return Host{}, ErrBadAddress
	}
	return Host{IP: ip, Port: port, DiscoveredAt: time.Now()}, nil
}

// ParseAddress turns what was typed into the server field into a host: an IP
// address or a name, either optionally followed by :port ([v6]:port for
// IPv6). A name is resolved, preferring IPv4, and kept as the host's name.
func ParseAddress(s string) (Host, error) {
	if h, err := ParseIPAddress(s); err == nil {
		return h, nil
	}
	name, port, err := splitAddress(s)
	if err != nil {
		return Host{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return Host{}, err
	}
	if len(addrs) == 0 {
		return Host{}, ErrBadAddress
	}
	ip := addrs[0].IP
	for _, a := range addrs {
		if a.IP.To4() != nil {
			ip = a.IP
			break
		}
	}
	return Host{IP: ip, Hostname: name, Port: port, DiscoveredAt: time.Now()}, nil
}
