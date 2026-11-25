package capsum

import (
	"sort"

	"prescreen.local/ps-2237843e01/pathlang"
)

// term is a conjunction (intersection) of generator languages, represented as
// a sorted, deduplicated set of generator ids. The empty term denotes the
// universal language: intersecting with nothing removes no words.
type term []int

func canonicalTerm(ids []int) term {
	sort.Ints(ids)
	out := make(term, 0, len(ids))
	for _, id := range ids {
		if len(out) == 0 || out[len(out)-1] != id {
			out = append(out, id)
		}
	}
	return out
}

func (a term) equal(b term) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// termLess gives a total order: shorter conjunctions first, then lexicographic.
func (a term) less(b term) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func intersectTerm(a, b term) term {
	merged := make([]int, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return canonicalTerm(merged)
}

// dnf is a disjunction (union) of conjunctions. An empty dnf is the empty
// language; a dnf containing the single empty term is the universal language.
type dnf []term

func canonicalDNF(ts []term) dnf {
	sort.Slice(ts, func(i, j int) bool { return ts[i].less(ts[j]) })
	out := make(dnf, 0, len(ts))
	for _, t := range ts {
		if len(out) > 0 && out[len(out)-1].equal(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (d dnf) equal(o dnf) bool {
	if len(d) != len(o) {
		return false
	}
	for i := range d {
		if !d[i].equal(o[i]) {
			return false
		}
	}
	return true
}

// unionDNF is lattice join: may grows by adding terms.
func unionDNF(a, b dnf) dnf {
	return canonicalDNF(append(append([]term(nil), a...), b...))
}

// intersectDNF distributes conjunction over disjunction:
// (∨ᵢ tᵢ) ∧ (∨ⱼ uⱼ) = ∨ᵢⱼ (tᵢ ∧ uⱼ).
func intersectDNF(a, b dnf) dnf {
	if len(a) == 0 || len(b) == 0 {
		return dnf{}
	}
	out := make([]term, 0, len(a)*len(b))
	for _, ta := range a {
		for _, tb := range b {
			out = append(out, intersectTerm(ta, tb))
		}
	}
	return canonicalDNF(out)
}

// universalDNF is the top of the language lattice.
func universalDNF() dnf { return dnf{term{}} }

// emptyDNF is the bottom of the language lattice.
func emptyDNF() dnf { return dnf{} }

// evaluate renders a dnf as an explicit finite DFA over the alphabet by
// intersecting generator DFAs inside each term and unioning across terms.
func (x *index) evaluate(d dnf) *pathlang.DFA {
	if len(d) == 0 {
		return pathlang.Empty(x.alphabet)
	}
	var acc *pathlang.DFA
	for _, t := range d {
		termLang := pathlang.Universe(x.alphabet)
		for _, gid := range t {
			termLang = pathlang.Intersect(termLang, x.generators[gid])
		}
		if acc == nil {
			acc = termLang
		} else {
			acc = pathlang.Union(acc, termLang)
		}
	}
	return pathlang.Minimize(acc)
}

// dnfFromGenerators builds the union language of the given generators, i.e.
// the authorization contributed by one or more framing domains.
func dnfFromGenerators(ids []int) dnf {
	if len(ids) == 0 {
		return emptyDNF()
	}
	terms := make([]term, 0, len(ids))
	for _, id := range ids {
		terms = append(terms, canonicalTerm([]int{id}))
	}
	return canonicalDNF(terms)
}
