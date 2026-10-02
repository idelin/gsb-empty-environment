// Package allen implements Allen's interval algebra: the thirteen basic
// relations between time intervals, their composition table, and
// path-consistency propagation (transitive closure) over temporal
// constraint networks.
package allen

import "strings"

// Relation is a set of basic interval relations encoded as a bitmask.
// Multiple bits denote a disjunction: any of the set basic relations
// may hold between the two intervals.
type Relation uint16

// The thirteen basic relations of Allen's interval algebra.
const (
	Before       Relation = 1 << iota // b
	Meets                             // m
	Overlaps                          // o
	Starts                            // s
	During                            // d
	Finishes                          // f
	Equal                             // eq
	FinishedBy                        // fi
	Contains                          // di
	StartedBy                         // si
	OverlappedBy                      // oi
	MetBy                             // mi
	After                             // bi
)

// Any is the universal relation: no information.
const Any Relation = 1<<13 - 1

var basics = []Relation{
	Before, Meets, Overlaps, Starts, During, Finishes, Equal,
	FinishedBy, Contains, StartedBy, OverlappedBy, MetBy, After,
}

var shortNames = map[Relation]string{
	Before: "b", Meets: "m", Overlaps: "o", Starts: "s", During: "d",
	Finishes: "f", Equal: "eq", FinishedBy: "fi", Contains: "di",
	StartedBy: "si", OverlappedBy: "oi", MetBy: "mi", After: "bi",
}

var fullNames = map[Relation]string{
	Before: "before", Meets: "meets", Overlaps: "overlaps",
	Starts: "starts", During: "during", Finishes: "finishes",
	Equal: "equal", FinishedBy: "finished-by", Contains: "contains",
	StartedBy: "started-by", OverlappedBy: "overlapped-by",
	MetBy: "met-by", After: "after",
}

// Basics returns the thirteen basic relations.
func Basics() []Relation { return append([]Relation(nil), basics...) }

// IsBasic reports whether r is exactly one basic relation.
func (r Relation) IsBasic() bool {
	return r != 0 && r&(r-1) == 0
}

// Name returns the full name of a basic relation, or the joined short
// codes for a disjunctive relation.
func (r Relation) Name() string {
	if r.IsBasic() {
		return fullNames[r]
	}
	return r.String()
}

func (r Relation) String() string {
	switch r {
	case 0:
		return "none"
	case Any:
		return "any"
	}
	var parts []string
	for _, b := range basics {
		if r&b != 0 {
			parts = append(parts, shortNames[b])
		}
	}
	return strings.Join(parts, "|")
}

func bitIndex(r Relation) int {
	i := 0
	for r >>= 1; r != 0; r >>= 1 {
		i++
	}
	return i
}

// basicBetween classifies the basic relation between interval X
// [sx,ex) and interval Y [sy,ey), given endpoint ranks.
func basicBetween(sx, ex, sy, ey int) Relation {
	switch {
	case ex < sy:
		return Before
	case ex == sy:
		return Meets
	case ey < sx:
		return After
	case ey == sx:
		return MetBy
	case sx == sy:
		switch {
		case ex < ey:
			return Starts
		case ex == ey:
			return Equal
		default:
			return StartedBy
		}
	case ex == ey:
		if sx > sy {
			return Finishes
		}
		return FinishedBy
	case sx < sy: // sy < ex, ex != ey
		if ex < ey {
			return Overlaps
		}
		return Contains
	default: // sx > sy, sx < ey, ex != ey
		if ex > ey {
			return OverlappedBy
		}
		return During
	}
}

var converseTable = map[Relation]Relation{
	Before: After, Meets: MetBy, Overlaps: OverlappedBy,
	Starts: StartedBy, During: Contains, Finishes: FinishedBy,
	Equal: Equal, FinishedBy: Finishes, Contains: During,
	StartedBy: Starts, OverlappedBy: Overlaps, MetBy: Meets,
	After: Before,
}

// Converse returns the relation with every basic relation replaced by
// its converse (swapping the roles of the two intervals).
func Converse(r Relation) Relation {
	var out Relation
	for _, b := range basics {
		if r&b != 0 {
			out |= converseTable[b]
		}
	}
	return out
}

// composition is the 13x13 composition table of basic relations.
// It is computed exactly at init time by enumerating every weak
// ordering of the six endpoints of three intervals: any consistent
// triple of basic relations has a realization, and every realization
// is captured by some ranking of the endpoints over {0..5}.
var composition = computeComposition()

func computeComposition() [13][13]Relation {
	var table [13][13]Relation
	for sx := 0; sx < 6; sx++ {
		for ex := 0; ex < 6; ex++ {
			if sx >= ex {
				continue
			}
			for sy := 0; sy < 6; sy++ {
				for ey := 0; ey < 6; ey++ {
					if sy >= ey {
						continue
					}
					for sz := 0; sz < 6; sz++ {
						for ez := 0; ez < 6; ez++ {
							if sz >= ez {
								continue
							}
							r1 := basicBetween(sx, ex, sy, ey)
							r2 := basicBetween(sy, ey, sz, ez)
							r3 := basicBetween(sx, ex, sz, ez)
							table[bitIndex(r1)][bitIndex(r2)] |= r3
						}
					}
				}
			}
		}
	}
	return table
}

// Compose returns the composition a ∘ b: every basic relation r that
// may hold between X and Z when X a Y and Y b Z for some interval Y.
func Compose(a, b Relation) Relation {
	var out Relation
	for _, r1 := range basics {
		if a&r1 == 0 {
			continue
		}
		for _, r2 := range basics {
			if b&r2 != 0 {
				out |= composition[bitIndex(r1)][bitIndex(r2)]
			}
		}
	}
	return out
}

// Local endpoint indices used by EndpointConstraint: X is the first
// interval, Y the second.
const (
	StartX = iota
	EndX
	StartY
	EndY
)

// EndpointConstraint states endpoint[To] - endpoint[From] <= 0
// (strictly less than 0 when Strict is set).
type EndpointConstraint struct {
	From, To int
	Strict   bool
}

func lt(p, q int) EndpointConstraint {
	return EndpointConstraint{From: q, To: p, Strict: true}
}

func eq(p, q int) []EndpointConstraint {
	return []EndpointConstraint{{From: q, To: p}, {From: p, To: q}}
}

var basicEndpointCons = map[Relation][]EndpointConstraint{
	Before:       {lt(EndX, StartY)},
	Meets:        eq(EndX, StartY),
	Overlaps:     {lt(StartX, StartY), lt(StartY, EndX), lt(EndX, EndY)},
	Starts:       append(eq(StartX, StartY), lt(EndX, EndY)),
	During:       {lt(StartY, StartX), lt(EndX, EndY)},
	Finishes:     append(eq(EndX, EndY), lt(StartY, StartX)),
	Equal:        append(eq(StartX, StartY), eq(EndX, EndY)...),
	FinishedBy:   append(eq(EndX, EndY), lt(StartX, StartY)),
	Contains:     {lt(StartX, StartY), lt(EndY, EndX)},
	StartedBy:    append(eq(StartX, StartY), lt(EndY, EndX)),
	OverlappedBy: {lt(StartY, StartX), lt(StartX, EndY), lt(EndY, EndX)},
	MetBy:        eq(EndY, StartX),
	After:        {lt(EndY, StartX)},
}

// EndpointConstraints returns the endpoint constraints implied by every
// basic relation in r, i.e. the constraints that hold no matter which
// disjunct of r is realized. For a singleton relation these are exactly
// the defining endpoint constraints of that basic relation.
func EndpointConstraints(r Relation) []EndpointConstraint {
	var list []Relation
	for _, b := range basics {
		if r&b != 0 {
			list = append(list, b)
		}
	}
	if len(list) == 0 {
		return nil
	}
	out := basicEndpointCons[list[0]]
	for _, b := range list[1:] {
		out = intersectEndpointCons(out, basicEndpointCons[b])
	}
	return out
}

func intersectEndpointCons(a, b []EndpointConstraint) []EndpointConstraint {
	var out []EndpointConstraint
	for _, ca := range a {
		for _, cb := range b {
			if ca.From == cb.From && ca.To == cb.To {
				out = append(out, EndpointConstraint{
					From:   ca.From,
					To:     ca.To,
					Strict: ca.Strict && cb.Strict,
				})
			}
		}
	}
	return out
}

// Network is an Allen interval-algebra constraint network over n
// unnamed intervals.
type Network struct {
	size int
	rel  [][]Relation
}

// NewNetwork creates a network over n intervals with no constraints.
func NewNetwork(size int) *Network {
	n := &Network{size: size, rel: make([][]Relation, size)}
	for i := range n.rel {
		n.rel[i] = make([]Relation, size)
		for j := range n.rel[i] {
			n.rel[i][j] = Any
		}
		n.rel[i][i] = Equal
	}
	return n
}

// Size returns the number of intervals in the network.
func (n *Network) Size() int { return n.size }

// Constrain intersects the relation between i and j with r, and the
// relation between j and i with the converse of r.
func (n *Network) Constrain(i, j int, r Relation) {
	n.rel[i][j] &= r
	n.rel[j][i] &= Converse(r)
}

// Relation returns the current relation set between i and j.
func (n *Network) Relation(i, j int) Relation { return n.rel[i][j] }

// Propagate enforces path consistency: for every triple (i,k,j) the
// relation R_ij is intersected with the composition R_ik ∘ R_kj,
// repeatedly, until a fixpoint (the transitive closure of the
// constraints) is reached. It returns false if some relation becomes
// empty, proving the network inconsistent.
//
// Note: as is well known, path consistency is sound but not complete
// for the full interval algebra; it detects most, but not all,
// inconsistencies.
func (n *Network) Propagate() bool {
	changed := true
	for changed {
		changed = false
		for i := 0; i < n.size; i++ {
			for j := 0; j < n.size; j++ {
				for k := 0; k < n.size; k++ {
					r := n.rel[i][j] & Compose(n.rel[i][k], n.rel[k][j])
					if r == 0 {
						return false
					}
					if r != n.rel[i][j] {
						n.rel[i][j] = r
						changed = true
					}
				}
			}
		}
	}
	return true
}
