package netsec

import "net"

// matchCIDR reports whether clientIP is covered by rule. rule is either a
// CIDR block ("10.0.0.0/8") or a bare address, which is treated as a host
// route (/32 for IPv4, /128 for IPv6). An unparseable rule or a nil client
// address never matches.
func matchCIDR(rule string, clientIP net.IP) bool {
	if clientIP == nil {
		return false
	}

	var network *net.IPNet
	if _, ipnet, err := net.ParseCIDR(rule); err == nil {
		network = ipnet
	} else if ip := net.ParseIP(rule); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		network = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
	} else {
		return false
	}

	// Normalize an IPv4-mapped IPv6 client to IPv4 so a 4-byte rule network
	// still matches it.
	if v4 := clientIP.To4(); v4 != nil && network.IP.To4() != nil {
		clientIP = v4
	}
	return network.Contains(clientIP)
}
