package scanner

import (
	"fmt"
	"net"
)

// ExpandCIDR returns all usable host addresses in the given CIDR block,
// excluding the network address and broadcast address.
func ExpandCIDR(cidr string) ([]net.IP, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}

	var ips []net.IP
	for cur := cloneIP(ip.Mask(ipnet.Mask)); ipnet.Contains(cur); inc(cur) {
		// Skip network address (all host bits = 0) and broadcast (all host bits = 1).
		if cur.Equal(ipnet.IP) {
			continue
		}
		if isBroadcast(cur, ipnet) {
			continue
		}
		ips = append(ips, cloneIP(cur))
	}
	return ips, nil
}

func cloneIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func inc(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func isBroadcast(ip net.IP, ipnet *net.IPNet) bool {
	broadcast := make(net.IP, len(ip))
	for i := range ip {
		broadcast[i] = ipnet.IP[i] | ^ipnet.Mask[i]
	}
	return ip.Equal(broadcast)
}
