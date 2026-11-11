package netsec

// Bound is one endpoint of an Interval. A zero Bound is the inclusive
// endpoint at the zero version; set Infinite for an unbounded side.
type Bound struct {
	V         Version
	Inclusive bool
	Infinite  bool // unbounded; V and Inclusive are ignored
}

// ClosedBound returns an inclusive endpoint at v.
func ClosedBound(v Version) Bound { return Bound{V: v, Inclusive: true} }

// OpenBound returns an exclusive endpoint at v.
func OpenBound(v Version) Bound { return Bound{V: v} }

// InfiniteBound returns an unbounded endpoint.
func InfiniteBound() Bound { return Bound{Infinite: true} }

// Interval is a single contiguous range of versions with independent
// openness at each endpoint. Either endpoint may be unbounded.
type Interval struct {
	Lo, Hi Bound
}

// isEmpty reports whether the interval contains no version at all. Besides
// inverted or equal-exclusive bounds, an open interval (a, b) is empty when
// a and b are adjacent versions.
func (iv Interval) isEmpty() bool {
	if iv.Lo.Infinite || iv.Hi.Infinite {
		return false
	}
	c := Compare(iv.Lo.V, iv.Hi.V)
	if c > 0 {
		return true
	}
	if c == 0 {
		return !(iv.Lo.Inclusive && iv.Hi.Inclusive)
	}
	if iv.Lo.Inclusive || iv.Hi.Inclusive {
		return false
	}
	return adjacent(iv.Lo.V, iv.Hi.V)
}

// isPoint reports whether the interval is exactly the single version v.
func (iv Interval) isPoint() (Version, bool) {
	if iv.Lo.Infinite || iv.Hi.Infinite {
		return Version{}, false
	}
	if iv.Lo.Inclusive && iv.Hi.Inclusive && iv.Lo.V.Equal(iv.Hi.V) {
		return iv.Lo.V, true
	}
	return Version{}, false
}

// Contains reports whether v lies inside the interval.
func (iv Interval) Contains(v Version) bool {
	if !iv.Lo.Infinite {
		c := Compare(v, iv.Lo.V)
		if c < 0 || (c == 0 && !iv.Lo.Inclusive) {
			return false
		}
	}
	if !iv.Hi.Infinite {
		c := Compare(v, iv.Hi.V)
		if c > 0 || (c == 0 && !iv.Hi.Inclusive) {
			return false
		}
	}
	return true
}

func (iv Interval) String() string {
	if p, ok := iv.isPoint(); ok {
		return "{" + p.String() + "}"
	}
	lo, hi := "(-∞", "(∞"
	if !iv.Lo.Infinite {
		lo = boundBracket(iv.Lo.V.String(), iv.Lo.Inclusive, true)
	}
	if !iv.Hi.Infinite {
		hi = boundBracket(iv.Hi.V.String(), iv.Hi.Inclusive, false)
	}
	return lo + ", " + hi
}

func boundBracket(v string, inclusive, lower bool) string {
	if lower {
		if inclusive {
			return "[" + v
		}
		return "(" + v
	}
	if inclusive {
		return v + "]"
	}
	return v + ")"
}

// loLess orders lower bounds: unbounded first, then by version, and for the
// same version an inclusive bound starts before an exclusive one.
func loLess(a, b Bound) bool {
	if a.Infinite || b.Infinite {
		return a.Infinite && !b.Infinite
	}
	if c := Compare(a.V, b.V); c != 0 {
		return c < 0
	}
	return a.Inclusive && !b.Inclusive
}

// hiLess orders upper bounds: by version, an exclusive bound ends before an
// inclusive one, and an unbounded bound is the greatest.
func hiLess(a, b Bound) bool {
	if a.Infinite || b.Infinite {
		return !a.Infinite && b.Infinite
	}
	if c := Compare(a.V, b.V); c != 0 {
		return c < 0
	}
	return !a.Inclusive && b.Inclusive
}

// touches reports whether an interval ending at hi and the next one starting
// at lo cover a contiguous range with no version left out, so normalization
// must merge them.
func touches(hi, lo Bound) bool {
	if hi.Infinite || lo.Infinite {
		return true
	}
	c := Compare(hi.V, lo.V)
	if c > 0 {
		return true
	}
	if c == 0 {
		return hi.Inclusive || lo.Inclusive
	}
	// hi.V < lo.V: contiguous only if both sides include their endpoint and
	// no version exists strictly in between.
	return hi.Inclusive && lo.Inclusive && adjacent(hi.V, lo.V)
}

// before reports whether an interval ending at hi lies strictly below an
// interval starting at lo, i.e. the two cannot overlap.
func before(hi, lo Bound) bool {
	if hi.Infinite || lo.Infinite {
		return false
	}
	c := Compare(hi.V, lo.V)
	if c < 0 {
		return true
	}
	if c == 0 {
		return !(hi.Inclusive && lo.Inclusive)
	}
	return false
}

func overlaps(a, b Interval) bool {
	return !before(a.Hi, b.Lo) && !before(b.Hi, a.Lo)
}

func maxLo(a, b Bound) Bound {
	if loLess(a, b) {
		return b
	}
	return a
}

func minHi(a, b Bound) Bound {
	if hiLess(a, b) {
		return a
	}
	return b
}

func maxHi(a, b Bound) Bound {
	if hiLess(a, b) {
		return b
	}
	return a
}

// subtract returns the pieces of a that remain after removing b.
func subtract(a, b Interval) []Interval {
	if !overlaps(a, b) {
		return []Interval{a}
	}
	var out []Interval
	if loLess(a.Lo, b.Lo) {
		out = append(out, Interval{Lo: a.Lo, Hi: Bound{V: b.Lo.V, Inclusive: !b.Lo.Inclusive, Infinite: b.Lo.Infinite}})
	}
	if hiLess(b.Hi, a.Hi) {
		out = append(out, Interval{Lo: Bound{V: b.Hi.V, Inclusive: !b.Hi.Inclusive, Infinite: b.Hi.Infinite}, Hi: a.Hi})
	}
	// Flipped bounds can collapse onto adjacent versions, leaving an empty
	// open interval; drop those.
	kept := out[:0]
	for _, iv := range out {
		if !iv.isEmpty() {
			kept = append(kept, iv)
		}
	}
	return kept
}
