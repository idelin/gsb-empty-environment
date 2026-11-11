package netsec

import "fmt"

// StepOutcome explains what happened to one concrete packet at one node.
type StepOutcome uint8

const (
	// OutcomePassed means the packet survived filters and translation.
	OutcomePassed StepOutcome = iota
	// OutcomeFiltered means an ordered filter rule dropped the packet.
	OutcomeFiltered
	// OutcomeOverflow means a translation overflowed the uint32 space.
	OutcomeOverflow
)

func (o StepOutcome) String() string {
	switch o {
	case OutcomePassed:
		return "passed"
	case OutcomeFiltered:
		return "filtered"
	case OutcomeOverflow:
		return "overflow"
	default:
		return fmt.Sprintf("outcome(%d)", int(o))
	}
}

func packetField(p Packet, f Field) uint32 {
	if f == Src {
		return p.Src
	}
	return p.Dst
}

func withPacketField(p Packet, f Field, v uint32) Packet {
	if f == Src {
		p.Src = v
	} else {
		p.Dst = v
	}
	return p
}

// applyFilter evaluates a node's ordered first-match filter list on one
// concrete packet. A node with no filters allows everything. Rules that do
// not match are skipped; the first matching rule's Allow verdict is final.
func applyFilter(n *Node, p Packet) (bool, string) {
	for _, rule := range n.Filters {
		if rule.Match.Contains(packetField(p, rule.Field)) {
			return rule.Allow, rule.Name
		}
	}
	return true, ""
}

// applyTranslate evaluates a node's ordered first-match translation list on
// one concrete packet. Both the signed 64-bit pre-check and the uint32
// post-check are enforced.
func applyTranslate(n *Node, p Packet) (Packet, StepOutcome, string) {
	for _, rule := range n.Translates {
		v := packetField(p, rule.Target)
		if !rule.Match.Contains(v) {
			continue
		}
		if rule.GuardActive && !rule.Guard.Contains(packetField(p, rule.GuardField)) {
			continue
		}
		next, ok := addChecked(v, rule.Delta)
		if !ok {
			return Packet{}, OutcomeOverflow, rule.Name
		}
		return withPacketField(p, rule.Target, next), OutcomePassed, rule.Name
	}
	return p, OutcomePassed, ""
}

// addChecked performs the two-stage integer overflow check required by the
// audit: the signed 64-bit intermediate must be representable (pre-check) and
// the final value must fit uint32 (post-check).
func addChecked(v uint32, delta int64) (uint32, bool) {
	sv := int64(v)
	if (delta > 0 && sv > int64(^uint32(0))-delta) ||
		(delta < 0 && sv < -delta) {
		return 0, false
	}
	sum := sv + delta
	if sum < 0 || sum > int64(^uint32(0)) {
		return 0, false
	}
	return uint32(sum), true
}

// stepPacket runs one concrete packet through one node: filters first, then
// translation, returning the post-node packet and the outcome.
func stepPacket(n *Node, p Packet) (Packet, StepOutcome, string) {
	if allow, rule := applyFilter(n, p); !allow {
		return Packet{}, OutcomeFiltered, rule
	}
	return applyTranslate(n, p)
}

// stepRect is the symbolic transfer function for one node. It mirrors
// stepPacket exactly over rectangular regions:
//
//  1. ordered first-match filters split the region into survivors and drops;
//  2. ordered first-match translation rules consume the surviving region,
//     splitting it into matched (shifted with overflow pieces dropped) and
//     residual (passed through unchanged) sub-regions.
func stepRect(n *Node, in Rect) (survivors []Rect, filtered, overflow []Rect) {
	survivors, filtered = filterRect(n, in)
	var residual []Rect
	residual = append(residual, survivors...)
	var passed []Rect
	for _, rule := range n.Translates {
		var nextResidual []Rect
		for _, r := range residual {
			matchField := r.field(rule.Target)
			hit, ok := intersectInterval(matchField, rule.Match)
			if !ok {
				nextResidual = append(nextResidual, r)
				continue
			}
			matched := r.withField(rule.Target, hit)
			nextResidual = append(nextResidual, r.subtract(matched)...)
			if rule.GuardActive {
				guardHit, guardOK := intersectInterval(matched.field(rule.GuardField), rule.Guard)
				if !guardOK {
					nextResidual = append(nextResidual, matched)
					continue
				}
				guarded := matched.withField(rule.GuardField, guardHit)
				nextResidual = append(nextResidual, matched.subtract(guarded)...)
				matched = guarded
			}
			shifted, bad := shiftRect(matched, rule)
			passed = append(passed, shifted...)
			overflow = append(overflow, bad...)
		}
		residual = canonicalize(nextResidual)
	}
	passed = append(passed, residual...)
	return canonicalize(passed), canonicalize(filtered), canonicalize(overflow)
}

// filterRect applies the ordered filter list to a rectangle set. Every
// rectangle is walked through the rules; the first intersecting rule claims
// the intersection (allow or drop), while the disjoint remainder continues.
func filterRect(n *Node, in Rect) (allowed, dropped []Rect) {
	pending := []Rect{in}
	for _, rule := range n.Filters {
		var next []Rect
		for _, r := range pending {
			hit, ok := intersectInterval(r.field(rule.Field), rule.Match)
			if !ok {
				next = append(next, r)
				continue
			}
			matched := r.withField(rule.Field, hit)
			rest := r.subtract(matched)
			if rule.Allow {
				allowed = append(allowed, matched)
			} else {
				dropped = append(dropped, matched)
			}
			next = append(next, rest...)
		}
		pending = canonicalize(next)
	}
	allowed = append(allowed, pending...)
	return canonicalize(allowed), canonicalize(dropped)
}

// shiftRect applies one translation rule to a region known to match it. The
// shifted field is split at the overflow boundary so every surviving piece
// stays inside uint32 while overflow pieces are reported separately.
func shiftRect(r Rect, rule TranslateRule) (safe []Rect, bad []Rect) {
	iv := r.field(rule.Target)
	safeIV, badIV := shiftInterval(iv, rule.Delta)
	for _, piece := range safeIV {
		safe = append(safe, r.withField(rule.Target, piece))
	}
	for _, piece := range badIV {
		bad = append(bad, r.withField(rule.Target, piece))
	}
	return safe, bad
}

// shiftInterval returns the in-bounds shifted pieces and the overflow pieces
// of iv under delta. It performs the same pre/post checks as addChecked but
// at interval granularity, preserving first-match concrete semantics.
func shiftInterval(iv Interval, delta int64) (safe, bad []Interval) {
	if delta == 0 {
		return []Interval{iv}, nil
	}
	split := splitAtOverflow(iv, delta)
	for _, piece := range split {
		lo, okLo := addChecked(piece.Lo, delta)
		hi, okHi := addChecked(piece.Hi, delta)
		if !okLo || !okHi {
			bad = append(bad, piece)
			continue
		}
		safe = append(safe, Interval{Lo: lo, Hi: hi})
	}
	return safe, bad
}

// splitAtOverflow cuts an interval at the largest prefix whose endpoints are
// on the same side of the overflow boundary. Because addChecked defines one
// contiguous safe prefix/suffix, at most two pieces are produced.
func splitAtOverflow(iv Interval, delta int64) []Interval {
	_, loOK := addChecked(iv.Lo, delta)
	_, hiOK := addChecked(iv.Hi, delta)
	if loOK == hiOK {
		return []Interval{iv}
	}
	maxU32 := int64(^uint32(0))
	if delta > 0 {
		boundary := uint32(maxU32 - delta)
		return []Interval{
			{Lo: iv.Lo, Hi: boundary},
			{Lo: boundary + 1, Hi: iv.Hi},
		}
	}
	boundary := uint32(-delta - 1)
	return []Interval{
		{Lo: iv.Lo, Hi: boundary},
		{Lo: boundary + 1, Hi: iv.Hi},
	}
}
