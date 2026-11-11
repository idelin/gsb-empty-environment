package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// Wildcard matches every value of a segment domain when used in
// SegmentFilter.Allow, and excludes every value when used in
// SegmentFilter.Deny.
const Wildcard = "*"

// enumerationLimit bounds the total tuple count below which Check
// cross-validates the decision diagram against exhaustive enumeration.
const enumerationLimit = 1 << 20

// Segment is one position of a resource tuple with its finite domain.
type Segment struct {
	Name   string
	Domain []string
}

// Spec describes the shape of resource tuples: one finite domain per
// segment, in tuple order.
type Spec struct {
	Segments []Segment
}

// SegmentFilter enumerates allowed and excluded values of one segment.
// The matched set is Allow minus Deny; an empty Allow matches nothing.
type SegmentFilter struct {
	Allow []string
	Deny  []string
}

// Pattern matches a tuple when every segment filter matches the
// corresponding tuple value. Segments must have exactly one filter per
// segment of the Spec.
type Pattern struct {
	Segments []SegmentFilter
}

// Policy is the published rule set: the visible set is the union of all
// Allow patterns minus the union of all Deny patterns. Explicit denies
// always take priority over allows.
type Policy struct {
	Allow []Pattern
	Deny  []Pattern
}

// Result reports the intersection of the visible set with the forbidden
// tenant set.
type Result struct {
	// Leaked is true when at least one forbidden tuple stays visible.
	Leaked bool
	// Count is the exact number of visible forbidden tuples.
	Count *big.Int
	// Witness is the lexicographically smallest (by domain order)
	// visible forbidden tuple, or nil when there is none.
	Witness []string
}

// Check computes the intersection of the policy's visible set with the
// forbidden tenant set using a multi-valued decision diagram, and returns
// the exact size of the intersection plus a minimal witness tuple.
//
// The Cartesian product of the domains is never expanded; when the
// product is small enough the diagram result is additionally verified
// against exhaustive enumeration.
func Check(spec Spec, pol Policy, forbidden []Pattern) (*Result, error) {
	domIndex, err := validateSpec(spec)
	if err != nil {
		return nil, err
	}
	resolve := func(p Pattern, what string) ([]map[int]bool, error) {
		return resolvePattern(spec, domIndex, p, what)
	}

	sizes := make([]int, len(spec.Segments))
	for i, seg := range spec.Segments {
		sizes[i] = len(seg.Domain)
	}
	eng := newMDD(sizes)

	allow := mddFalse
	for i, p := range pol.Allow {
		sets, err := resolve(p, fmt.Sprintf("allow pattern %d", i))
		if err != nil {
			return nil, err
		}
		allow = eng.apply(opOr, allow, eng.buildSets(sets))
	}
	deny := mddFalse
	for i, p := range pol.Deny {
		sets, err := resolve(p, fmt.Sprintf("deny pattern %d", i))
		if err != nil {
			return nil, err
		}
		deny = eng.apply(opOr, deny, eng.buildSets(sets))
	}
	visible := eng.apply(opAndNot, allow, deny)

	forb := mddFalse
	for i, p := range forbidden {
		sets, err := resolve(p, fmt.Sprintf("forbidden pattern %d", i))
		if err != nil {
			return nil, err
		}
		forb = eng.apply(opOr, forb, eng.buildSets(sets))
	}
	leak := eng.apply(opAnd, visible, forb)

	res := &Result{Count: eng.count(leak)}
	res.Leaked = res.Count.Sign() > 0
	if res.Leaked {
		path, ok := eng.minWitness(leak)
		if !ok {
			return nil, errors.New("netsec: internal error: non-empty intersection without witness")
		}
		res.Witness = make([]string, len(path))
		for i, v := range path {
			res.Witness[i] = spec.Segments[i].Domain[v]
		}
	}

	if totalTuples(spec).Cmp(big.NewInt(enumerationLimit)) <= 0 {
		if err := verifyEnumeration(spec, pol, forbidden, eng, leak, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func validateSpec(spec Spec) ([]map[string]int, error) {
	if len(spec.Segments) == 0 {
		return nil, errors.New("netsec: spec must declare at least one segment")
	}
	domIndex := make([]map[string]int, len(spec.Segments))
	for i, seg := range spec.Segments {
		if len(seg.Domain) == 0 {
			return nil, fmt.Errorf("netsec: segment %q has an empty domain", seg.Name)
		}
		idx := make(map[string]int, len(seg.Domain))
		for j, v := range seg.Domain {
			if _, dup := idx[v]; dup {
				return nil, fmt.Errorf("netsec: segment %q repeats domain value %q", seg.Name, v)
			}
			idx[v] = j
		}
		domIndex[i] = idx
	}
	return domIndex, nil
}

// resolvePattern compiles a pattern into per-segment sets of allowed
// domain value indices.
func resolvePattern(spec Spec, domIndex []map[string]int, p Pattern, what string) ([]map[int]bool, error) {
	if len(p.Segments) != len(spec.Segments) {
		return nil, fmt.Errorf("netsec: %s has %d segment filters, want %d",
			what, len(p.Segments), len(spec.Segments))
	}
	sets := make([]map[int]bool, len(p.Segments))
	for i, f := range p.Segments {
		set, err := resolveFilter(f, spec.Segments[i], domIndex[i], what)
		if err != nil {
			return nil, err
		}
		sets[i] = set
	}
	return sets, nil
}

func resolveFilter(f SegmentFilter, seg Segment, idx map[string]int, what string) (map[int]bool, error) {
	out := make(map[int]bool, len(f.Allow))
	for _, v := range f.Allow {
		if v == Wildcard {
			for i := range seg.Domain {
				out[i] = true
			}
			continue
		}
		i, ok := idx[v]
		if !ok {
			return nil, fmt.Errorf("netsec: %s allows unknown value %q in segment %q", what, v, seg.Name)
		}
		out[i] = true
	}
	for _, v := range f.Deny {
		if v == Wildcard {
			clear(out)
			continue
		}
		i, ok := idx[v]
		if !ok {
			return nil, fmt.Errorf("netsec: %s denies unknown value %q in segment %q", what, v, seg.Name)
		}
		delete(out, i)
	}
	return out, nil
}

func totalTuples(spec Spec) *big.Int {
	total := big.NewInt(1)
	for _, seg := range spec.Segments {
		total.Mul(total, big.NewInt(int64(len(seg.Domain))))
	}
	return total
}
