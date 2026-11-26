package bgpsim

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
)

// NodeID is the stable identifier of a topology node (border router,
// route reflector or client).
type NodeID string

// ClusterID is a route-reflection cluster identifier. It is matched
// literally against the CLUSTER_LIST carried by advertisements.
type ClusterID string

// RouterID is a BGP Router-ID. It accepts either an IPv4 dotted quad
// ("10.0.0.1") or a decimal 32-bit value ("167772161"). Comparisons use
// the numeric value; values that cannot be parsed fall back to lexical
// ordering so the simulation stays total and deterministic.
type RouterID string

// CompareRouterID reports the ordering of two RouterIDs:
// -1 if a < b, 0 if equal and +1 if a > b.
func CompareRouterID(a, b RouterID) int {
	na, oka := parseRouterID(string(a))
	nb, okb := parseRouterID(string(b))
	if oka && okb {
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(string(a), string(b))
}

func parseRouterID(s string) (uint32, bool) {
	if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return binary.BigEndian.Uint32(v4), true
		}
	}
	if n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32); err == nil {
		return uint32(n), true
	}
	return 0, false
}
