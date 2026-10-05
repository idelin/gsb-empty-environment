package fd

import "fmt"

// Derivability is the proof (or refutation) that a single dependency
// follows from a dependency set.
type Derivability struct {
	FD        FD
	Derivable bool
	Trace     []Step // evidence when Derivable
}

// EquivalenceResult reports whether two dependency sets cover each
// other, with per-dependency evidence in both directions.
type EquivalenceResult struct {
	Equivalent bool
	// FG[i] proves G's i-th dependency from F (after RHS splitting).
	FG []Derivability
	// GF[i] proves F's i-th dependency from G (after RHS splitting).
	GF []Derivability
}

// CheckEquivalent decides F ≡ G by testing mutual implication: every
// (split) dependency of G must be derivable from F and vice versa.
// Each derivable dependency carries its closure trace as evidence;
// each underivable one is a concrete counterexample.
func CheckEquivalent(F, G []FD) EquivalenceResult {
	fg := checkDirection(F, G)
	gf := checkDirection(G, F)
	res := EquivalenceResult{Equivalent: true, FG: fg, GF: gf}
	for _, d := range fg {
		if !d.Derivable {
			res.Equivalent = false
		}
	}
	for _, d := range gf {
		if !d.Derivable {
			res.Equivalent = false
		}
	}
	return res
}

// checkDirection splits each dependency of dst into single-attribute
// RHS form and tests derivability under src.
func checkDirection(src, dst []FD) []Derivability {
	var out []Derivability
	for _, f := range dst {
		for _, r := range f.RHS {
			single := NewFD(f.LHS, []string{r})
			closure, trace := ClosureTrace(f.LHS, src)
			out = append(out, Derivability{
				FD:        single,
				Derivable: Contains(closure, r),
				Trace:     trace,
			})
		}
	}
	return out
}

// CoverEquivalent reports whether fds and its canonical cover imply
// each other — a self-check that normalization preserved semantics.
func CoverEquivalent(original []FD, cover CoverResult) bool {
	return CheckEquivalent(original, cover.Cover).Equivalent
}

// Summary renders the equivalence verdict with evidence.
func (r EquivalenceResult) Summary() string {
	var s string
	if r.Equivalent {
		s = "EQUIVALENT: each set implies the other\n"
	} else {
		s = "NOT EQUIVALENT\n"
	}
	s += "\nF |= G:\n"
	for _, d := range r.FG {
		s += "  " + derivabilityLine(d) + "\n"
	}
	s += "G |= F:\n"
	for _, d := range r.GF {
		s += "  " + derivabilityLine(d) + "\n"
	}
	return s
}

func derivabilityLine(d Derivability) string {
	if d.Derivable {
		return fmt.Sprintf("[ok]   %s  (%d derivation step(s))", d.FD, len(d.Trace))
	}
	return fmt.Sprintf("[FAIL] %s  -- counterexample, not derivable", d.FD)
}
