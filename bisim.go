package main

import "sort"

// Pair is a state pair (s in A, t in B).
type Pair struct{ S, T int }

// Witness records why a pair was removed from the candidate relation:
// the attacker move (in A if Side == 0, in B if Side == 1) that the
// defender could not match with a weak move.
type Witness struct {
	Side  int
	Label string
	From  int
	To    int
}

// Result holds the outcome of a weak bisimulation check.
type Result struct {
	Equivalent bool
	Pairs      []Pair // the maximal weak bisimulation, sorted
	rank       map[Pair]int
	witness    map[Pair]Witness
}

// Check computes the maximal weak bisimulation between a and b by greatest
// fixpoint iteration. Removals within one iteration are computed against a
// snapshot of the relation, so the rank of a removed pair strictly exceeds
// the rank of every pair its witness depends on; this guarantees that
// distinguishing-process extraction terminates.
func Check(a, b *LTS) *Result {
	inR := make(map[Pair]bool)
	for s := 0; s < a.N; s++ {
		for t := 0; t < b.N; t++ {
			inR[Pair{s, t}] = true
		}
	}
	rank := make(map[Pair]int)
	wit := make(map[Pair]Witness)
	for k := 1; ; k++ {
		type removal struct {
			p Pair
			w Witness
		}
		var doomed []removal
		for _, p := range sortedPairs(inR) {
			if w, fails := failsWeak(a, b, inR, p); fails {
				doomed = append(doomed, removal{p, w})
			}
		}
		if len(doomed) == 0 {
			break
		}
		for _, d := range doomed {
			delete(inR, d.p)
			rank[d.p] = k
			wit[d.p] = d.w
		}
	}
	pairs := sortedPairs(inR)
	return &Result{
		Equivalent: inR[Pair{a.Start, b.Start}],
		Pairs:      pairs,
		rank:       rank,
		witness:    wit,
	}
}

// failsWeak checks the weak transfer conditions for pair p against relation
// inR. For every strong move s -a-> s' (a possibly tau) the defender must
// answer with a weak move t =â=> t' (â empty for tau) such that (s', t')
// stays in the relation; symmetrically for the other side.
func failsWeak(a, b *LTS, inR map[Pair]bool, p Pair) (Witness, bool) {
	for _, e := range a.Out[p.S] {
		matched := false
		for _, tp := range b.WeakTargets(p.T, e.Label) {
			if inR[Pair{e.To, tp}] {
				matched = true
				break
			}
		}
		if !matched {
			return Witness{Side: 0, Label: e.Label, From: e.From, To: e.To}, true
		}
	}
	for _, e := range b.Out[p.T] {
		matched := false
		for _, sp := range a.WeakTargets(p.S, e.Label) {
			if inR[Pair{sp, e.To}] {
				matched = true
				break
			}
		}
		if !matched {
			return Witness{Side: 1, Label: e.Label, From: e.From, To: e.To}, true
		}
	}
	return Witness{}, false
}

func sortedPairs(m map[Pair]bool) []Pair {
	ps := make([]Pair, 0, len(m))
	for p := range m {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].S != ps[j].S {
			return ps[i].S < ps[j].S
		}
		return ps[i].T < ps[j].T
	})
	return ps
}
