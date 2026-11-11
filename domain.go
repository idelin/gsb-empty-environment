package netsec

import (
	"fmt"
	"sort"
)

// NarrowedDomain is a finite, sorted subset of the uint32 address space that
// is closed under every translation delta of the graph: adding any rule
// delta to a domain value that stays in uint32 yields another domain value.
// Enumerating concrete packets over this domain replays the exact original
// rule constants rather than a remapped mini model, so witnesses are real
// uint32 packets.
type NarrowedDomain struct {
	Values []uint32
	index  map[uint32]int
}

// Size returns the number of distinct address values in the domain.
func (d *NarrowedDomain) Size() int { return len(d.Values) }

// Contains reports whether v is in the narrowed domain.
func (d *NarrowedDomain) Contains(v uint32) bool {
	_, ok := d.index[v]
	return ok
}

// DomainConfig bounds the construction of a narrowed domain.
type DomainConfig struct {
	// MaxValues caps the number of distinct addresses in the domain. Zero
	// uses the package default.
	MaxValues int
}

// DefaultDomainMax is the default ceiling for a narrowed domain. At this size
// a two-field exhaustive enumeration explores up to MaxValues^2 packets,
// which is deliberately small enough for audit-grade cross-checking while
// still leaving room for delta closure of realistic NAT rule sets.
const DefaultDomainMax = 256

// ErrDomainTooLarge reports that delta closure exceeds the configured budget.
var ErrDomainTooLarge = fmt.Errorf("netsec: narrowed domain exceeds configured budget")

// BuildNarrowedDomain constructs a finite closed address sub-domain.
//
// Seed values are 0, MaxUint32 and every interval boundary appearing in the
// graph's filters, translations, ingress and egress constraints. The seed set
// is then closed under every translation delta: while a (value, delta) pair
// has an in-bounds result, that result is added. Closure is what makes
// exhaustive enumeration on the small domain faithful to the full semantics,
// because no packet can be shifted to an address outside the domain.
func BuildNarrowedDomain(g *Graph, ingress, egress []Constraint, cfg DomainConfig) (*NarrowedDomain, error) {
	max := cfg.MaxValues
	if max <= 0 {
		max = DefaultDomainMax
	}
	set := map[uint32]struct{}{
		0:          {},
		^uint32(0): {},
	}
	for _, c := range ingress {
		addSeed(set, c.Range)
	}
	for _, c := range egress {
		addSeed(set, c.Range)
	}
	for _, n := range g.Nodes {
		for _, fr := range n.Filters {
			addSeed(set, fr.Match)
		}
		for _, tr := range n.Translates {
			addSeed(set, tr.Match)
			if tr.GuardActive {
				addSeed(set, tr.Guard)
			}
		}
	}

	work := make([]uint32, 0, len(set))
	for v := range set {
		work = append(work, v)
	}
	for len(work) > 0 {
		v := work[0]
		work = work[1:]
		for _, n := range g.Nodes {
			for _, rule := range n.Translates {
				if !rule.Match.Contains(v) {
					continue
				}
				next, ok := addChecked(v, rule.Delta)
				if !ok {
					continue
				}
				if _, seen := set[next]; seen {
					continue
				}
				if len(set) >= max {
					return nil, fmt.Errorf("%w: reached %d values while adding %d (rule %q delta %d)",
						ErrDomainTooLarge, max, next, rule.Name, rule.Delta)
				}
				set[next] = struct{}{}
				work = append(work, next)
			}
		}
	}

	values := make([]uint32, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	idx := make(map[uint32]int, len(values))
	for i, v := range values {
		idx[v] = i
	}
	return &NarrowedDomain{Values: values, index: idx}, nil
}

func addSeed(set map[uint32]struct{}, iv Interval) {
	set[iv.Lo] = struct{}{}
	set[iv.Hi] = struct{}{}
	if iv.Lo > 0 {
		set[iv.Lo-1] = struct{}{}
	}
	if iv.Hi < ^uint32(0) {
		set[iv.Hi+1] = struct{}{}
	}
	// Interior values must be represented too: an enumeration domain that
	// only keeps interval boundaries cannot distinguish "part of a match"
	// from "gap between two boundary points". When the interval is small
	// enough, seed every value; large intervals rely on boundary points plus
	// delta closure to bound the exhaustive budget.
	if iv.Size() <= uint64(interiorSeedLimit) {
		for v := iv.Lo; ; v++ {
			set[v] = struct{}{}
			if v == iv.Hi {
				break
			}
		}
	}
}

const interiorSeedLimit = 256

// restrictRect clips a rectangle to values present in the domain and returns
// the concrete (src, dst) pairs it contains.
func (d *NarrowedDomain) packetsIn(r Rect) []Packet {
	var out []Packet
	for _, s := range d.Values {
		if !r.S.Contains(s) {
			continue
		}
		for _, dv := range d.Values {
			if r.D.Contains(dv) {
				out = append(out, Packet{Src: s, Dst: dv})
			}
		}
	}
	return out
}

// packetsMatching returns all domain packets satisfying a constraint list.
func (d *NarrowedDomain) packetsMatching(cs []Constraint) []Packet {
	return d.packetsIn(rectFromConstraints(cs))
}
