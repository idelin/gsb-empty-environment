package pathcheck

import "sort"

// Verdict is the outcome of Check. The zero value is Unknown so an
// unfinished computation never looks conclusive.
type Verdict int

const (
	// Unknown means the enumeration budget was exhausted before the
	// question could be settled.
	Unknown Verdict = iota
	// Witness means some path is allowed and not denied; Result.Path
	// holds a witness with the minimum number of segments.
	Witness
	// None means every allowed path is denied (the allow language is
	// contained in the deny language).
	None
)

func (v Verdict) String() string {
	switch v {
	case Witness:
		return "witness"
	case None:
		return "none"
	default:
		return "unknown"
	}
}

// Result is the outcome of Check.
type Result struct {
	Verdict Verdict
	Path    Path // the minimal witness; valid when Verdict == Witness
	Checked int  // number of candidate paths examined
	Bound   int  // segment-depth bound that certifies a None verdict
}

// Check reports whether some path matches at least one allow pattern and
// no deny pattern. It enumerates candidate paths over a finite alphabet —
// the literals occurring in the patterns plus one fresh symbol standing
// for "any other literal" — in increasing segment-count order, so the
// first hit is a witness with the minimum number of segments.
//
// Enumeration is complete up to a depth bound derived from the pattern
// sizes (see depthBound): if no witness exists, none can exist at any
// depth. If the budget of candidate paths is exhausted before the bound is
// reached, the verdict is Unknown.
func Check(allows, denies []Pattern, budget int) Result {
	res := Result{Verdict: Unknown, Bound: depthBound(allows, denies)}
	if len(allows) == 0 {
		res.Verdict = None
		return res
	}
	alpha := alphabet(allows, denies)
	checked := 0
	for depth := 0; depth <= res.Bound; depth++ {
		for _, rooted := range []bool{false, true} {
			idx := make([]int, depth)
			for {
				path := Path{Rooted: rooted, Segs: make([]string, depth)}
				for i, a := range idx {
					path.Segs[i] = alpha[a]
				}
				if checked >= budget {
					res.Checked = checked
					return res
				}
				checked++
				if matchesAny(allows, path) && !matchesAny(denies, path) {
					res.Verdict = Witness
					res.Path = path
					res.Checked = checked
					return res
				}
				i := depth - 1
				for ; i >= 0; i-- {
					idx[i]++
					if idx[i] < len(alpha) {
						break
					}
					idx[i] = 0
				}
				if i < 0 {
					break
				}
			}
		}
	}
	res.Verdict = None
	res.Checked = checked
	return res
}

func matchesAny(pats []Pattern, path Path) bool {
	for _, p := range pats {
		if p.Matches(path) {
			return true
		}
	}
	return false
}

// alphabet returns the finite enumeration alphabet: every literal used by
// the patterns, sorted, plus one fresh symbol that represents any literal
// not mentioned anywhere. Matching only ever tests equality against the
// mentioned literals, so replacing an unmentioned literal with the fresh
// symbol never changes a match result; hence a witness over this alphabet
// exists iff one exists over arbitrary literals, with the same segment
// count.
func alphabet(allows, denies []Pattern) []string {
	seen := map[string]bool{}
	var lits []string
	add := func(p Pattern) {
		for _, s := range p.Segs {
			if s.Kind == Lit && !seen[s.Lit] {
				seen[s.Lit] = true
				lits = append(lits, s.Lit)
			}
		}
	}
	for _, p := range allows {
		add(p)
	}
	for _, p := range denies {
		add(p)
	}
	sort.Strings(lits)
	fresh := "\x00other"
	for seen[fresh] {
		fresh += "\x00"
	}
	return append(lits, fresh)
}

// depthBound returns a segment count B such that, whenever an allowed and
// not denied path exists, one exists with at most B segments.
//
// Each pattern is an NFA with len(Segs)+1 positions; the union of m allow
// patterns determinises to at most 2^a states with a = sum(len+1), and the
// complement of the deny union to at most 2^d states. The product automaton
// therefore has at most 2^(a+d) states, and its shortest accepted word —
// per rootedness — has fewer segments than that. The value saturates at
// 2^30; the enumeration budget will turn the verdict Unknown long before
// such depths are reached.
func depthBound(allows, denies []Pattern) int {
	total := 0
	for _, p := range allows {
		total += len(p.Segs) + 1
	}
	for _, p := range denies {
		total += len(p.Segs) + 1
	}
	const maxBound = 1 << 30
	bound := 1
	for i := 0; i < total && bound <= maxBound/2; i++ {
		bound *= 2
	}
	return bound
}
