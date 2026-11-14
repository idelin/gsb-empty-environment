package netsec

import (
	"fmt"
	"net"
	"strings"
)

func normalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return append(net.IP(nil), v4...)
	}
	return append(net.IP(nil), ip.To16()...)
}

// parseIP accepts a bare address, an address with a port, and bracketed IPv6.
// It returns a normalized address without a zone or port.
func parseIP(value string) (net.IP, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty address")
	}
	if strings.EqualFold(value, "unknown") || strings.EqualFold(value, "_hidden") {
		return nil, fmt.Errorf("obfuscated address %q", value)
	}

	if strings.HasPrefix(value, "[") {
		host, _, err := net.SplitHostPort(value)
		if err != nil {
			host = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
		}
		ip := parseIPHost(host)
		if ip == nil {
			return nil, fmt.Errorf("invalid address %q", value)
		}
		return ip, nil
	}

	if ip := parseIPHost(value); ip != nil {
		return ip, nil
	}

	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q", value)
	}
	ip := parseIPHost(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid address %q", value)
	}
	return ip, nil
}

func parseIPHost(host string) net.IP {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	return normalizeIP(ip)
}

// ParseClientIP parses an address as it appears in RemoteAddr or a forwarding
// header. Ports and IPv6 zones are removed.
func ParseClientIP(value string) (net.IP, error) {
	return parseIP(value)
}

// CanonicalClientIP returns the normalized textual form of value.
func CanonicalClientIP(value string) (string, error) {
	ip, err := parseIP(value)
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}
