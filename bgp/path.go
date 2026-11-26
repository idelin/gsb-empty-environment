package bgp

import (
	"errors"
	"net/netip"
)

// ASPathLength returns the standard AS_PATH length used in the best-path
// algorithm:

// - every AS in an AS_SEQUENCE counts 1,
// - an entire AS_SET counts exactly 1 regardless of its contents,
// - AS_CONFED_SEQUENCE and AS_CONFED_SET count 0 (RFC 5065 section 3).
func ASPathLength(path []Segment) int {
	total := 0
	for _, seg := range path {
		switch seg.Type {
		case SegASSequence:
			total += len(seg.ASes)
		case SegASSet:
			if len(seg.ASes) > 0 {
				total++
			}
		case SegConfedSequence, SegConfedSet:
			// Confederation segments are invisible outside the confederation.
		}
	}
	return total
}

// NeighborAS extracts the neighbouring autonomous system: the first AS of the
// first non-confederation segment. Leading AS_CONFED_* segments are skipped.
//
// For an AS_SEQUENCE the left-most AS is returned. For an AS_SET the smallest
// AS in the set is used as the deterministic group key (sets carry no order).
// A path whose AS_PATH contains no non-confederation segment (for example a
// pure iBGP-learned route) returns 0, which is the reserved "internal" group.
func NeighborAS(path []Segment) uint32 {
	for _, seg := range path {
		if seg.IsConfederation() || len(seg.ASes) == 0 {
			continue
		}
		switch seg.Type {
		case SegASSequence:
			return seg.ASes[0]
		case SegASSet:
			min := seg.ASes[0]
			for _, as := range seg.ASes[1:] {
				if as < min {
					min = as
				}
			}
			return min
		}
	}
	return 0
}

// containsAS reports whether the given AS appears in segments of the requested
// kind (confederation vs. externally visible).
func containsAS(path []Segment, as uint32, confederation bool) bool {
	for _, seg := range path {
		if seg.IsConfederation() != confederation {
			continue
		}
		for _, candidate := range seg.ASes {
			if candidate == as {
				return true
			}
		}
	}
	return false
}

// effectiveMED resolves the MED used for comparison, applying the missing-MED
// policy of the selector.
func (c *Config) effectiveMED(p *Path) uint32 {
	if p.MEDPresent {
		return p.MED
	}
	if c.MissingMED == MissingMEDAsInfinity {
		return InfiniteMED
	}
	return 0
}

// validate runs the mandatory legality filters. It returns RejectNone when the
// path is eligible for best-path selection.
func (c *Config) validate(p *Path) RejectReason {
	if !p.NextHopReachable {
		return RejectNextHopUnreachable
	}
	// Externally visible loop: local AS must never appear in AS_SEQUENCE/SET.
	if containsAS(p.ASPath, c.LocalAS, false) {
		return RejectASLoop
	}
	// Confederation-internal loop: our own member AS must not appear in an
	// AS_CONFED_* segment.
	if c.MemberAS != 0 && containsAS(p.ASPath, c.MemberAS, true) {
		return RejectConfedASLoop
	}
	if c.RouterID != 0 && p.OriginatorID == c.RouterID {
		return RejectOriginatorLoop
	}
	if c.ClusterID != 0 {
		for _, cluster := range p.ClusterList {
			if cluster == c.ClusterID {
				return RejectClusterLoop
			}
		}
	}
	return RejectNone
}

// ParseRouterID parses a dotted-quad BGP identifier (e.g. "10.0.0.1") into its
// numeric 32-bit form, suitable for Config.RouterID, ClusterID and Path IDs.
func ParseRouterID(s string) (uint32, error) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return 0, err
	}
	if !addr.Is4() {
		return 0, errors.New("bgp: router identifier must be an IPv4 dotted-quad")
	}
	arr := addr.As4()
	return uint32(arr[0])<<24 | uint32(arr[1])<<16 | uint32(arr[2])<<8 | uint32(arr[3]), nil
}

// FormatRouterID renders a numeric identifier back into dotted-quad form.
func FormatRouterID(id uint32) string {
	return netip.AddrFrom4([4]byte{
		byte(id >> 24),
		byte(id >> 16),
		byte(id >> 8),
		byte(id),
	}).String()
}

// CompareIP returns -1/0/1 comparing two textual IP addresses. IPv4 and IPv6
// are compared numerically within their own address family; an IPv4 address
// sorts before an IPv6 address. Unparseable addresses sort after valid ones
// and are then ordered lexicographically so the comparator stays total.
func CompareIP(a, b string) int {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	switch {
	case errA != nil && errB != nil:
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		default:
			return 0
		}
	case errA != nil:
		return 1
	case errB != nil:
		return -1
	}
	if pa.Is4() != pb.Is4() {
		if pa.Is4() {
			return -1
		}
		return 1
	}
	return pa.Compare(pb)
}
