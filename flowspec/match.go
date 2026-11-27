package flowspec

import (
	"bytes"
	"net"
)

// Packet is the concrete header tuple used by Resolve/RuleMatchesPacket. All
// numeric fields are widened to uint64 to fit every FlowSpec value width.
type Packet struct {
	DstIP        [16]byte
	SrcIP        [16]byte
	HasDstIP     bool
	HasSrcIP     bool
	Protocol     uint64
	DstPort      uint64
	SrcPort      uint64
	ICMPType     uint64
	ICMPCode     uint64
	TCPFlags     uint64
	PacketLength uint64
	DSCP         uint64
	Fragment     uint64
}

// SetRelation describes the match-set relationship between two rules or
// components.
type SetRelation uint8

const (
	// RelDisjoint means the two match sets can never match the same packet.
	RelDisjoint SetRelation = iota
	// RelOverlap means the sets intersect but neither contains the other.
	RelOverlap
	// RelACoversB means B's match set is a subset of A's.
	RelACoversB
	// RelBCoversA means A's match set is a subset of B's.
	RelBCoversA
	// RelEqual means the match sets are identical.
	RelEqual
	// RelUnknown means the decidable subset solver cannot prove the relation;
	// callers conservatively treat it as a potential overlap.
	RelUnknown
)

// RuleMatchesPacket reports whether pkt satisfies every component of the rule
// (components are ANDed together).
func RuleMatchesPacket(r *Rule, pkt *Packet) bool {
	if r == nil || pkt == nil {
		return false
	}
	for i := range r.Components {
		if !componentMatchesPacket(&r.Components[i], pkt) {
			return false
		}
	}
	return true
}

// SetDstIP sets the destination address from a net.IP.
func (p *Packet) SetDstIP(ip net.IP) {
	copy(p.DstIP[:], ip.To16())
	p.HasDstIP = true
}

// SetSrcIP sets the source address from a net.IP.
func (p *Packet) SetSrcIP(ip net.IP) {
	copy(p.SrcIP[:], ip.To16())
	p.HasSrcIP = true
}

func componentMatchesPacket(c *Component, pkt *Packet) bool {
	switch c.Type {
	case TypeDestPrefix:
		if !pkt.HasDstIP {
			return false
		}
		return c.Prefix.Contains(bytes16ToIP(pkt.DstIP))
	case TypeSourcePrefix:
		if !pkt.HasSrcIP {
			return false
		}
		return c.Prefix.Contains(bytes16ToIP(pkt.SrcIP))
	}

	// Type 4 "port" matches source OR destination port.
	if c.Type == TypePort {
		return evalComponentTerms(c, pkt.DstPort) || evalComponentTerms(c, pkt.SrcPort)
	}

	value, ok := packetField(c.Type, pkt)
	if !ok {
		return false
	}
	return evalComponentTerms(c, value)
}

func bytes16ToIP(a [16]byte) net.IP { return net.IP(a[:]) }

func evalComponentTerms(c *Component, value uint64) bool {
	if len(c.Terms) == 0 {
		return false
	}
	result := termMatches(c.Terms[0], c.Type, value)
	for i := 1; i < len(c.Terms); i++ {
		next := termMatches(c.Terms[i], c.Type, value)
		if c.Terms[i-1].And {
			result = result && next
		} else {
			result = result || next
		}
	}
	return result
}

func termMatches(t Term, ctype ComponentType, value uint64) bool {
	if ctype.IsBitmask() {
		return t.MatchesBitmask(value)
	}
	return t.MatchesNumeric(value)
}

func packetField(t ComponentType, p *Packet) (uint64, bool) {
	switch t {
	case TypeProtocol:
		return p.Protocol, true
	case TypeDestPort:
		return p.DstPort, true
	case TypeSourcePort:
		return p.SrcPort, true
	case TypeICMPType:
		return p.ICMPType, true
	case TypeICMPCode:
		return p.ICMPCode, true
	case TypeTCPFlags:
		return p.TCPFlags, true
	case TypePacketLength:
		return p.PacketLength, true
	case TypeDSCP:
		return p.DSCP, true
	case TypeFragment:
		return p.Fragment, true
	default:
		return 0, false
	}
}

// MatchRelation computes the match-set relation between two whole rules.
func MatchRelation(a, b *Rule) SetRelation {
	if a == nil || b == nil {
		return RelUnknown
	}
	aCoversB := true
	bCoversA := true
	equal := true

	for t := TypeDestPrefix; t <= TypeFragment; t++ {
		ca := a.Component(t)
		cb := b.Component(t)
		switch {
		case ca == nil && cb == nil:
			continue
		case ca == nil:
			// A wildcard at t: A_t is universal and covers B_t.
			bCoversA = false
			equal = false
			continue
		case cb == nil:
			// B wildcard at t: B_t is universal; A_t cannot cover it.
			aCoversB = false
			equal = false
			continue
		}

		rel := componentRelation(ca, cb)
		switch rel {
		case RelDisjoint:
			return RelDisjoint
		case RelEqual:
			// keep flags
		case RelACoversB:
			bCoversA = false
			equal = false
		case RelBCoversA:
			aCoversB = false
			equal = false
		default: // Overlap or Unknown: no containment/equality claim
			aCoversB = false
			bCoversA = false
			equal = false
		}
	}

	switch {
	case equal:
		return RelEqual
	case aCoversB:
		return RelACoversB
	case bCoversA:
		return RelBCoversA
	default:
		return RelOverlap
	}
}

func componentRelation(a, b *Component) SetRelation {
	if bytes.Equal(componentWire(a), componentWire(b)) {
		return RelEqual
	}
	if a.Type.IsPrefix() {
		return prefixRelation(&a.Prefix, &b.Prefix)
	}
	if a.Type.IsBitmask() {
		return bitmaskRelation(a, b)
	}
	return numericRelation(a, b)
}

func prefixRelation(a, b *net.IPNet) SetRelation {
	ao, _ := a.Mask.Size()
	bo, _ := b.Mask.Size()
	aNet := a.IP.Mask(a.Mask)
	bNet := b.IP.Mask(b.Mask)

	aCoversB := bo >= ao && a.Contains(bNet)
	bCoversA := ao >= bo && b.Contains(aNet)

	switch {
	case ao == bo && bytes.Equal(aNet.To16(), bNet.To16()):
		return RelEqual
	case aCoversB && bCoversA:
		return RelEqual
	case aCoversB:
		return RelACoversB
	case bCoversA:
		return RelBCoversA
	case !a.Contains(bNet) && !b.Contains(aNet):
		return RelDisjoint
	default:
		return RelOverlap
	}
}

// bitmaskConstraint extracts a single masked-equality constraint
// ((field & care) == value) for the common one-term Match case.
func bitmaskConstraint(c *Component) (care, value uint64, ok bool) {
	if len(c.Terms) != 1 {
		return 0, 0, false
	}
	t := c.Terms[0]
	if t.Not || t.Less || t.Greater {
		return 0, 0, false
	}
	if t.Match {
		// (v & m) == m  -> care=m, required value=m (all care bits set).
		return t.Value, t.Value, true
	}
	// Any-bit term is a union over bits; not a single equality constraint.
	return 0, 0, false
}

func bitmaskRelation(a, b *Component) SetRelation {
	ca, va, oka := bitmaskConstraint(a)
	cb, vb, okb := bitmaskConstraint(b)
	if !oka || !okb {
		return RelUnknown
	}
	shared := ca & cb
	if shared != 0 && ((va^vb)&shared) != 0 {
		return RelDisjoint
	}
	aCoversB := (ca & ^cb) == 0 && ((va^vb)&ca) == 0
	bCoversA := (cb & ^ca) == 0 && ((va^vb)&cb) == 0
	switch {
	case aCoversB && bCoversA:
		return RelEqual
	case aCoversB:
		return RelACoversB
	case bCoversA:
		return RelBCoversA
	default:
		return RelOverlap
	}
}

// numericSet returns the accepted value set when every term is a plain
// (possibly OR-ed) equality.
func numericSet(c *Component) (map[uint64]bool, bool) {
	set := make(map[uint64]bool)
	for _, t := range c.Terms {
		if t.Not || t.Greater || t.Less || !t.Match {
			return nil, false
		}
		set[t.Value] = true
	}
	return set, true
}

// interval is an inclusive numeric range [lo, hi].
type interval struct {
	lo, hi uint64
	empty  bool
}

// termInterval maps a single relational term to an inclusive interval.
func termInterval(t Term) (interval, bool) {
	const maxU64 = ^uint64(0)
	switch t.Relation() {
	case CmpEqual:
		return interval{t.Value, t.Value, false}, true
	case CmpNotEqual:
		return interval{}, false
	case CmpGT:
		if t.Value == maxU64 {
			return interval{empty: true}, true
		}
		return interval{t.Value + 1, maxU64, false}, true
	case CmpGTE:
		return interval{t.Value, maxU64, false}, true
	case CmpLT:
		if t.Value == 0 {
			return interval{empty: true}, true
		}
		return interval{0, t.Value - 1, false}, true
	case CmpLTE:
		return interval{0, t.Value, false}, true
	default:
		return interval{}, false
	}
}

// numericInterval folds AND-joined relational terms into one interval.
func numericInterval(c *Component) (interval, bool) {
	if len(c.Terms) == 0 {
		return interval{}, false
	}
	iv := interval{0, ^uint64(0), false}
	for i, t := range c.Terms {
		one, ok := termInterval(t)
		if !ok {
			return interval{}, false
		}
		// Terms after the first must be AND-joined with their predecessor.
		if i > 0 && !c.Terms[i-1].And {
			return interval{}, false
		}
		iv = intersectInterval(iv, one)
		if iv.empty {
			return iv, true
		}
	}
	return iv, true
}

func intersectInterval(a, b interval) interval {
	if a.empty || b.empty {
		return interval{empty: true}
	}
	lo := a.lo
	if b.lo > lo {
		lo = b.lo
	}
	hi := a.hi
	if b.hi < hi {
		hi = b.hi
	}
	if lo > hi {
		return interval{empty: true}
	}
	return interval{lo, hi, false}
}

func numericRelation(a, b *Component) SetRelation {
	sa, aSetOK := numericSet(a)
	sb, bSetOK := numericSet(b)

	switch {
	case aSetOK && bSetOK:
		return setRelation(sa, sb)
	}

	ia, aIvOK := numericInterval(a)
	ib, bIvOK := numericInterval(b)

	switch {
	case aIvOK && bIvOK:
		return intervalRelation(ia, ib)
	case aSetOK && bIvOK:
		// A is finite set, B interval: ask how set sits in interval.
		return setVsInterval(sa, ib, true)
	case aIvOK && bSetOK:
		return setVsInterval(sb, ia, false)
	default:
		return RelUnknown
	}
}

func setRelation(a, b map[uint64]bool) SetRelation {
	aInB := 0
	for v := range a {
		if b[v] {
			aInB++
		}
	}
	bInA := 0
	for v := range b {
		if a[v] {
			bInA++
		}
	}
	switch {
	case len(a) == aInB && len(b) == bInA:
		return RelEqual
	case len(b) == bInA: // every b element in a -> B subset A -> A covers B
		return RelACoversB
	case len(a) == aInB: // every a element in b -> A subset B -> B covers A
		return RelBCoversA
	case aInB == 0:
		return RelDisjoint
	default:
		return RelOverlap
	}
}

func intervalRelation(a, b interval) SetRelation {
	if a.empty || b.empty || a.hi < b.lo || b.hi < a.lo {
		return RelDisjoint
	}
	aCoversB := a.lo <= b.lo && b.hi <= a.hi
	bCoversA := b.lo <= a.lo && a.hi <= b.hi
	switch {
	case aCoversB && bCoversA:
		return RelEqual
	case aCoversB:
		return RelACoversB
	case bCoversA:
		return RelBCoversA
	default:
		return RelOverlap
	}
}

// setVsInterval relates a finite set s to interval iv. setIsA indicates the
// set belongs to side A (true) or side B (false).
func setVsInterval(s map[uint64]bool, iv interval, setIsA bool) SetRelation {
	if iv.empty {
		return RelDisjoint
	}
	inside := 0
	for v := range s {
		if v >= iv.lo && v <= iv.hi {
			inside++
		}
	}
	allInside := inside == len(s)
	switch {
	case inside == 0:
		return RelDisjoint
	case allInside:
		// set is fully contained in interval.
		if setIsA {
			return RelBCoversA // A(set) subset B(interval)
		}
		return RelACoversB // B(set) subset A(interval)
	default:
		return RelOverlap
	}
}
