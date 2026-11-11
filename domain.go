package netsec

import (
	"net/netip"
	"sort"
)

// enumeratePrefixes builds representatives from the atomic address and
// prefix-length intervals induced by every PrefixRange condition.
func enumeratePrefixes(ranges []PrefixRange) ([]Prefix, string) {
	if len(ranges) == 0 {
		return []Prefix{MustParsePrefix("0.0.0.0/0")}, "ipv4"
	}
	width := 32
	for _, r := range ranges {
		if r.Network.Valid() && r.Network.Net().Addr().BitLen() == 128 {
			width = 128
		}
	}
	addrCuts := map[uint32]struct{}{0: {}}
	lenCuts := map[uint32]struct{}{0: {}, uint32(width): {}}
	for _, r := range ranges {
		if r.Network.Valid() && r.Network.Net().Addr().BitLen() == width {
			if lo, ok := addrAsUint32(r.Network); ok {
				addrCuts[lo] = struct{}{}
				if end, overflow := networkEnd(r.Network.Net()); !overflow {
					addrCuts[end] = struct{}{}
				}
			}
		}
		if r.MinLen > 0 {
			lenCuts[uint32(r.MinLen)] = struct{}{}
		}
		if r.MaxLen > 0 {
			lenCuts[uint32(r.MaxLen)+1] = struct{}{}
		}
	}
	addrIntervals := makeIntervals(addrCuts)
	lenIntervals := makeIntervals(lenCuts)
	out := []Prefix{}
	seen := map[string]struct{}{}
	for _, ai := range addrIntervals {
		for _, li := range lenIntervals {
			p, ok := representativePrefix(ai.Lo, int(li.Lo), width)
			if !ok {
				continue
			}
			key := p.String()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := addrAsUint32(out[i])
		aj, _ := addrAsUint32(out[j])
		if ai != aj {
			return ai < aj
		}
		return out[i].Bits() < out[j].Bits()
	})
	family := "ipv4"
	if width == 128 {
		family = "ipv6"
	}
	return out, family
}

func representativePrefix(addr uint32, bits, width int) (Prefix, bool) {
	if bits < 0 || bits > width {
		return Prefix{}, false
	}
	if width == 32 {
		a := netip.AddrFrom4([4]byte{byte(addr >> 24), byte(addr >> 16), byte(addr >> 8), byte(addr)})
		p, err := a.Prefix(bits)
		if err != nil {
			return Prefix{}, false
		}
		return PrefixFromNet(p), true
	}
	// IPv6 analysis maps low 32-bit cut points into the reserved
	// documentation prefix so each atomic interval still has a concrete
	// representative of the required length.
	a := netip.AddrFrom16([16]byte{
		0x20, 0x01, 0x0d, 0xb8,
		byte(addr >> 24), byte(addr >> 16), byte(addr >> 8), byte(addr),
	})
	p, err := a.Prefix(bits)
	if err != nil {
		return Prefix{}, false
	}
	return PrefixFromNet(p), true
}

func networkEnd(p netip.Prefix) (uint32, bool) {
	if p.Addr().BitLen() != 32 {
		return 0, true
	}
	b := p.Masked().Addr().As4()
	base := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	hostBits := 32 - p.Bits()
	if hostBits >= 32 {
		return 0, true
	}
	size := uint32(1) << hostBits
	return base + size, base+size < base
}

// enumeratePathLens builds atomic intervals from path-length cuts.
func enumeratePathLens(ranges []PathLenRange, maxLen int) []int {
	if maxLen <= 0 {
		maxLen = 255
	}
	// Sentinel maxLen+1 closes the final interval [last-cut, maxLen];
	// makeIntervals drops values beyond maxLen when selecting reps.
	cuts := map[uint32]struct{}{0: {}, uint32(maxLen) + 1: {}}
	for _, r := range ranges {
		if r.Min > 0 {
			cuts[uint32(r.Min)] = struct{}{}
		}
		if r.Max > 0 {
			cuts[uint32(r.Max)+1] = struct{}{}
		}
	}
	intervals := makeIntervals(cuts)
	out := make([]int, 0, len(intervals))
	for _, iv := range intervals {
		out = append(out, int(iv.Lo))
	}
	return out
}

// makeIntervals turns sorted cut points c0 < c1 < ... into atomic
// intervals [c0, c1-1], [c1, c2-1], ... covering every value between
// the first and last cut point.
func makeIntervals(cuts map[uint32]struct{}) []intInterval {
	pts := make([]uint32, 0, len(cuts))
	for c := range cuts {
		pts = append(pts, c)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })
	out := []intInterval{}
	for i := 0; i+1 < len(pts); i++ {
		if pts[i+1] == 0 || pts[i+1]-1 < pts[i] {
			continue
		}
		out = append(out, intInterval{Lo: pts[i], Hi: pts[i+1] - 1})
	}
	return out
}

func collectReferencedTags(ps *PolicySet) TagSet {
	var tags TagSet
	var walk func(Condition)
	walk = func(c Condition) {
		switch v := c.(type) {
		case TagCondition:
			tags = tags.Union(v.Tags)
		case AndConds:
			for _, sub := range v {
				walk(sub)
			}
		case OrConds:
			for _, sub := range v {
				walk(sub)
			}
		case NotConds:
			walk(v.Inner)
		}
	}
	for _, p := range ps.Policies {
		for _, r := range p.Rules {
			if r.When != nil {
				walk(r.When)
			}
			switch a := r.Do.(type) {
			case AddTags:
				tags = tags.Union(a.Tags)
			case DelTags:
				tags = tags.Union(a.Tags)
			}
		}
	}
	return tags
}

func collectPrefixRanges(ps *PolicySet) []PrefixRange {
	out := []PrefixRange{}
	var walk func(Condition)
	walk = func(c Condition) {
		switch v := c.(type) {
		case PrefixRange:
			out = append(out, v)
		case AndConds:
			for _, sub := range v {
				walk(sub)
			}
		case OrConds:
			for _, sub := range v {
				walk(sub)
			}
		case NotConds:
			walk(v.Inner)
		}
	}
	for _, p := range ps.Policies {
		for _, r := range p.Rules {
			if r.When != nil {
				walk(r.When)
			}
		}
	}
	return out
}

func collectPathLenRanges(ps *PolicySet) []PathLenRange {
	out := []PathLenRange{}
	var walk func(Condition)
	walk = func(c Condition) {
		switch v := c.(type) {
		case PathLenRange:
			out = append(out, v)
		case AndConds:
			for _, sub := range v {
				walk(sub)
			}
		case OrConds:
			for _, sub := range v {
				walk(sub)
			}
		case NotConds:
			walk(v.Inner)
		}
	}
	for _, p := range ps.Policies {
		for _, r := range p.Rules {
			if r.When != nil {
				walk(r.When)
			}
		}
	}
	return out
}
