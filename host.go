package netsec

import (
	"encoding/binary"
	"net"
	"path"
	"strings"
)

// hostAllowed applies rsync's precedence rules: hosts deny is checked first;
// a request that matches neither list is denied when hosts allow is non-empty.
func hostAllowed(module Module, host string) bool {
	denyPatterns := splitConfigList(module.HostsDeny)
	allowPatterns := splitConfigList(module.HostsAllow)

	host = normalizeHost(host)
	if hostMatchesAny(denyPatterns, host) {
		return false
	}
	if len(allowPatterns) == 0 {
		return true
	}
	return hostMatchesAny(allowPatterns, host)
}

func hostMatchesAny(patterns []string, host string) bool {
	for _, pattern := range patterns {
		if hostMatches(pattern, host) {
			return true
		}
	}
	return false
}

func hostMatches(pattern, host string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	pattern = strings.Trim(pattern, "[]")
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	if pattern == "*" {
		return true
	}

	hostIP := net.ParseIP(host)
	if _, network, err := net.ParseCIDR(pattern); err == nil && network != nil {
		return hostIP != nil && network.Contains(hostIP)
	}
	if network, mask, ok := parseNetmask(pattern); ok {
		return hostIP != nil && ipInNetwork(hostIP, network, mask)
	}

	if strings.ContainsAny(pattern, "*?[") {
		matched, err := path.Match(pattern, host)
		return err == nil && matched
	}
	return pattern == host
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}

	if strings.HasPrefix(host, "[") {
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		} else {
			host = strings.Trim(host, "[]")
		}
	} else if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}

	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

// parseNetmask accepts the older "network/netmask" form in addition to CIDR.
func parseNetmask(pattern string) (net.IP, net.IP, bool) {
	networkText, maskText, ok := strings.Cut(pattern, "/")
	if !ok {
		return nil, nil, false
	}
	network := net.ParseIP(networkText)
	mask := net.ParseIP(maskText)
	if network == nil || mask == nil || len(network) != len(mask) {
		return nil, nil, false
	}
	return network, mask, true
}

func ipInNetwork(ip, network, mask net.IP) bool {
	if len(ip) != len(network) || len(ip) != len(mask) {
		return false
	}

	if len(ip) == net.IPv4len {
		ip4 := ip.To4()
		network4 := network.To4()
		mask4 := mask.To4()
		if ip4 == nil || network4 == nil || mask4 == nil {
			return false
		}
		ipValue := binary.BigEndian.Uint32(ip4)
		networkValue := binary.BigEndian.Uint32(network4)
		maskValue := binary.BigEndian.Uint32(mask4)
		return ipValue&maskValue == networkValue&maskValue
	}

	ip16 := ip.To16()
	network16 := network.To16()
	mask16 := mask.To16()
	if ip16 == nil || network16 == nil || mask16 == nil {
		return false
	}
	for i := range ip16 {
		if ip16[i]&mask16[i] != network16[i]&mask16[i] {
			return false
		}
	}
	return true
}
