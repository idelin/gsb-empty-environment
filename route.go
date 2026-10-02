package rib

import (
	"net/netip"
	"slices"
)

// Route is one entry under a prefix. Higher Priority wins; ties break by
// insertion order (earlier first). NextHops is an ECMP-style group.
type Route struct {
	Priority int
	NextHops []netip.Addr
	Tag      string

	seq uint64 // insertion sequence, for stable tie-breaking
}

func sameRoute(a, b Route) bool {
	return a.Priority == b.Priority && a.Tag == b.Tag && slices.Equal(a.NextHops, b.NextHops)
}

// better reports whether a sorts ahead of b (best-first order).
func better(a, b Route) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.seq < b.seq
}

// insertRoute returns a new sorted slice with r added; identical routes dedup.
func insertRoute(rs []Route, r Route) []Route {
	for _, e := range rs {
		if sameRoute(e, r) {
			return rs
		}
	}
	out := make([]Route, 0, len(rs)+1)
	i := 0
	for i < len(rs) && better(rs[i], r) {
		out = append(out, rs[i])
		i++
	}
	out = append(out, r)
	return append(out, rs[i:]...)
}

// removeRoutes returns a new slice with all routes equal to r removed.
func removeRoutes(rs []Route, r Route) ([]Route, bool) {
	out := make([]Route, 0, len(rs))
	found := false
	for _, e := range rs {
		if sameRoute(e, r) {
			found = true
			continue
		}
		out = append(out, e)
	}
	return out, found
}

func hasRoute(rs []Route, r Route) bool {
	for _, e := range rs {
		if sameRoute(e, r) {
			return true
		}
	}
	return false
}

// SelectNextHop deterministically picks a next hop from the group by flow hash.
func (r Route) SelectNextHop(flow uint64) (netip.Addr, bool) {
	if len(r.NextHops) == 0 {
		return netip.Addr{}, false
	}
	return r.NextHops[flow%uint64(len(r.NextHops))], true
}
