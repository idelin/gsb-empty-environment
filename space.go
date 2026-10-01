package main

import "net/netip"

// rect is a hyper-rectangle in (dst address, protocol, dst port) space.
// Address bounds are inclusive and always in one address family.
type rect struct {
	lo, hi   netip.Addr
	pLo, pHi int
	dLo, dHi int
}

// ruleRect maps a rule onto its header-space rectangle.
func ruleRect(r Rule) rect {
	p := r.Prefix.Masked()
	pLo, pHi := 0, 255
	if r.Protocol != ProtoAny {
		pLo, pHi = r.Protocol, r.Protocol
	}
	return rect{lo: p.Addr(), hi: lastAddr(p), pLo: pLo, pHi: pHi, dLo: r.PortLo, dHi: r.PortHi}
}

// lastAddr returns the last address of a masked prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	bits := p.Bits()
	if p.Addr().Is4() {
		b := p.Addr().As4()
		for i := bits; i < 32; i++ {
			b[i/8] |= 1 << uint(7-i%8)
		}
		return netip.AddrFrom4(b)
	}
	b := p.Addr().As16()
	for i := bits; i < 128; i++ {
		b[i/8] |= 1 << uint(7-i%8)
	}
	return netip.AddrFrom16(b)
}

func maxAddr(a, b netip.Addr) netip.Addr {
	if a.Compare(b) >= 0 {
		return a
	}
	return b
}

func minAddr(a, b netip.Addr) netip.Addr {
	if a.Compare(b) <= 0 {
		return a
	}
	return b
}

// intersect returns the overlap of two rectangles. Rectangles from
// different address families never intersect.
func intersect(a, b rect) (rect, bool) {
	if a.lo.Is4() != b.lo.Is4() {
		return rect{}, false
	}
	r := rect{
		lo:  maxAddr(a.lo, b.lo),
		hi:  minAddr(a.hi, b.hi),
		pLo: max(a.pLo, b.pLo),
		pHi: min(a.pHi, b.pHi),
		dLo: max(a.dLo, b.dLo),
		dHi: min(a.dHi, b.dHi),
	}
	if r.lo.Compare(r.hi) > 0 || r.pLo > r.pHi || r.dLo > r.dHi {
		return rect{}, false
	}
	return r, true
}

// subtract removes b from a and returns the remaining rectangles
// (0 to 6 of them). Returns nil when b fully covers a.
func subtract(a, b rect) []rect {
	inter, ok := intersect(a, b)
	if !ok {
		return []rect{a}
	}
	var out []rect
	// Address dimension: parts strictly below/above the intersection.
	if inter.lo.Compare(a.lo) > 0 {
		out = append(out, rect{a.lo, inter.lo.Prev(), a.pLo, a.pHi, a.dLo, a.dHi})
	}
	if inter.hi.Compare(a.hi) < 0 {
		out = append(out, rect{inter.hi.Next(), a.hi, a.pLo, a.pHi, a.dLo, a.dHi})
	}
	// Protocol dimension within the intersection's address slice.
	if inter.pLo > a.pLo {
		out = append(out, rect{inter.lo, inter.hi, a.pLo, inter.pLo - 1, a.dLo, a.dHi})
	}
	if inter.pHi < a.pHi {
		out = append(out, rect{inter.lo, inter.hi, inter.pHi + 1, a.pHi, a.dLo, a.dHi})
	}
	// Port dimension within the intersection's addr+proto slice.
	if inter.dLo > a.dLo {
		out = append(out, rect{inter.lo, inter.hi, inter.pLo, inter.pHi, a.dLo, inter.dLo - 1})
	}
	if inter.dHi < a.dHi {
		out = append(out, rect{inter.lo, inter.hi, inter.pLo, inter.pHi, inter.dHi + 1, a.dHi})
	}
	return out
}

// remainder returns the parts of r not covered by any blocker.
// An empty result means r is fully covered by the union of blockers.
func remainder(r rect, blockers []rect) []rect {
	rest := []rect{r}
	for _, b := range blockers {
		var next []rect
		for _, x := range rest {
			next = append(next, subtract(x, b)...)
		}
		rest = next
		if len(rest) == 0 {
			return nil
		}
	}
	return rest
}
