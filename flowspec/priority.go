package flowspec

import (
	"bytes"
	"sort"
)

// NLRIKey returns the canonical unsigned-byte NLRI representation used by the
// RFC 8955 §5 deterministic comparison. It prefers canonical re-encoding and
// falls back to concatenated raw component bytes.
func NLRIKey(r *Rule) []byte {
	if r == nil {
		return nil
	}
	if b, err := r.EncodeBare(); err == nil && len(b) > 0 {
		return b
	}
	var buf bytes.Buffer
	for i := range r.Components {
		buf.Write(r.Components[i].Raw)
	}
	return buf.Bytes()
}

// CompareWire compares two NLRIs as lexicographic unsigned byte strings (the
// primitive underlying the RFC binary ordering). It returns -1/0/+1 in the
// bytes.Compare direction.
func CompareWire(a, b *Rule) int {
	return bytes.Compare(NLRIKey(a), NLRIKey(b))
}

// ComparePriority compares FlowSpec precedence. It returns -1 when a has
// higher precedence (sorts first), +1 when b has higher precedence and 0 when
// their match specification is tie-equal.
//
// It follows the RFC 8955 component-wise ordering: component types are walked
// in ascending order; the earliest type present in only one rule makes that
// rule more specific; prefix components resolve by longest prefix length (the
// longest-match rule required for correct blackhole-vs-permit analysis); equal
// specificity falls back to the canonical unsigned component encoding so the
// order is total and deterministic. Actions never affect precedence.
func ComparePriority(a, b *Rule) int {
	if a == b {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	aAF, bAF := a.effectiveAF(), b.effectiveAF()
	if aAF != bAF {
		if aAF < bAF {
			return -1
		}
		return 1
	}
	for t := TypeDestPrefix; t <= TypeFragment; t++ {
		ca := a.Component(t)
		cb := b.Component(t)
		switch {
		case ca == nil && cb == nil:
			continue
		case ca != nil && cb == nil:
			return -1 // a constrains the earliest differing component
		case ca == nil && cb != nil:
			return 1
		}
		if c := compareComponentPriority(ca, cb); c != 0 {
			return c
		}
	}
	return 0
}

// effectiveAF returns the configured address family, inferring it from a
// prefix component when a host left AF at its zero value.
func (r *Rule) effectiveAF() AFI {
	if r == nil {
		return AFIPv4
	}
	if r.AF != 0 {
		return r.AF
	}
	if p, ok := r.DestPrefix(); ok {
		if _, bits := p.Mask.Size(); bits == 128 {
			return AFIPv6
		}
		return AFIPv4
	}
	if p, ok := r.SourcePrefix(); ok {
		if _, bits := p.Mask.Size(); bits == 128 {
			return AFIPv6
		}
	}
	return AFIPv4
}

func compareComponentPriority(a, b *Component) int {
	if a.Type.IsPrefix() {
		ao, ab := a.Prefix.Mask.Size()
		bo, bb := b.Prefix.Mask.Size()
		_ = ab
		_ = bb
		if ao != bo {
			if ao > bo {
				return -1 // longer prefix = higher precedence
			}
			return 1
		}
		ai := a.Prefix.IP.To16()
		bi := b.Prefix.IP.To16()
		if c := bytes.Compare(ai, bi); c != 0 {
			return c
		}
		return 0
	}
	return bytes.Compare(componentWire(a), componentWire(b))
}

func componentWire(c *Component) []byte {
	if len(c.Raw) > 0 {
		return c.Raw
	}
	b, err := EncodeComponents([]Component{*c}, guessAF(c))
	if err != nil {
		return nil
	}
	return b
}

func guessAF(c *Component) AFI {
	if c.Type.IsPrefix() {
		if _, bits := c.Prefix.Mask.Size(); bits == 128 {
			return AFIPv6
		}
	}
	return AFIPv4
}

// PriorityOrder returns rule indices in highest-precedence-first order. Ties
// retain the input order (stable), keeping results deterministic.
func PriorityOrder(rules []*Rule) []int {
	idx := make([]int, len(rules))
	for i := range rules {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return ComparePriority(rules[idx[i]], rules[idx[j]]) < 0
	})
	return idx
}

// SortByPriority returns a new slice ordered highest-precedence-first.
func SortByPriority(rules []*Rule) []*Rule {
	order := PriorityOrder(rules)
	out := make([]*Rule, len(rules))
	for pos, i := range order {
		out[pos] = rules[i]
	}
	return out
}
