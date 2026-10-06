package automata

import "fmt"

// Relation is a binary relation on states, stored as an adjacency matrix.
type Relation struct {
	n   int
	mat [][]bool
}

func newRelation(n int) *Relation {
	m := make([][]bool, n)
	for i := range m {
		m[i] = make([]bool, n)
	}
	return &Relation{n: n, mat: m}
}

// Holds reports whether (p, q) is in the relation.
func (r *Relation) Holds(p, q int) bool { return r.mat[p][q] }

// BisimulationFixedPoint computes the greatest bisimulation relation on the
// automaton directly, as the greatest fixed point of the operator
//
//	F(R) = { (p,q) | out(p) = out(q) and for every label l and every
//	        equivalence class C of R:
//	          |{ l-successors of p in C }| = |{ l-successors of q in C }| }
//
// (successors counted with multiplicity). Starting from the universal
// output-respecting relation, each iterate is an equivalence relation and
// the chain descends to the greatest fixed point, i.e. the coarsest
// bisimulation "in relational form". This is the independent, direct model
// used to cross-check the partition algorithm.
func BisimulationFixedPoint(a *Automaton) *Relation {
	n := a.NumStates
	succ := a.succIndex()
	labels := a.Labels()

	r := newRelation(n)
	for p := 0; p < n; p++ {
		for q := 0; q < n; q++ {
			r.mat[p][q] = a.Outputs[p] == a.Outputs[q]
		}
	}

	for {
		classes := equivalenceClasses(r)
		classOf := make([]int, n)
		for c, members := range classes {
			for _, s := range members {
				classOf[s] = c
			}
		}
		next := newRelation(n)
		changed := false
		for p := 0; p < n; p++ {
			for q := 0; q < n; q++ {
				ok := r.mat[p][q] && matchingCounts(succ[p], succ[q], labels, classOf)
				next.mat[p][q] = ok
				if ok != r.mat[p][q] {
					changed = true
				}
			}
		}
		if !changed {
			return r
		}
		r = next
	}
}

// matchingCounts reports whether p and q have, for every label, identical
// per-class successor multiplicities.
func matchingCounts(succP, succQ map[string]map[int]int, labels []string, classOf []int) bool {
	for _, l := range labels {
		cp := countByClass(succP[l], classOf)
		cq := countByClass(succQ[l], classOf)
		if len(cp) != len(cq) {
			return false
		}
		for c, n := range cp {
			if cq[c] != n {
				return false
			}
		}
	}
	return true
}

func countByClass(targets map[int]int, classOf []int) map[int]int {
	counts := make(map[int]int)
	for to, mult := range targets {
		counts[classOf[to]] += mult
	}
	return counts
}

// equivalenceClasses returns the classes of r, which must be an equivalence
// relation. Classes are ordered by smallest member.
func equivalenceClasses(r *Relation) [][]int {
	seen := make([]bool, r.n)
	var classes [][]int
	for s := 0; s < r.n; s++ {
		if seen[s] {
			continue
		}
		var class []int
		for t := 0; t < r.n; t++ {
			if r.mat[s][t] {
				class = append(class, t)
				seen[t] = true
			}
		}
		classes = append(classes, class)
	}
	return classes
}

// VerifyAgainstFixedPoint cross-checks the partition against the directly
// computed greatest fixed point of the bisimulation operator. It returns an
// error describing the first discrepancy found, or nil if the partition is
// exactly the set of equivalence classes of the greatest bisimulation.
func (p *Partition) VerifyAgainstFixedPoint(a *Automaton) error {
	rel := BisimulationFixedPoint(a)

	// 1. The fixed-point relation must be an equivalence.
	for x := 0; x < a.NumStates; x++ {
		if !rel.mat[x][x] {
			return fmt.Errorf("verify: fixed point not reflexive at state %d", x)
		}
		for y := 0; y < a.NumStates; y++ {
			if rel.mat[x][y] != rel.mat[y][x] {
				return fmt.Errorf("verify: fixed point not symmetric at (%d,%d)", x, y)
			}
			for z := 0; z < a.NumStates; z++ {
				if rel.mat[x][y] && rel.mat[y][z] && !rel.mat[x][z] {
					return fmt.Errorf("verify: fixed point not transitive at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}

	// 2. The partition must coincide with the relation's classes.
	for x := 0; x < a.NumStates; x++ {
		for y := 0; y < a.NumStates; y++ {
			sameBlock := p.BlockOf[x] == p.BlockOf[y]
			if sameBlock != rel.mat[x][y] {
				return fmt.Errorf("verify: states %d,%d: partition says same=%v, fixed point says %v",
					x, y, sameBlock, rel.mat[x][y])
			}
		}
	}
	return nil
}

// VerifyQuotient checks that the quotient graph is well defined: every state
// in a block induces exactly the same block-level edges (with multiplicities)
// as the block representative used to build the quotient.
func (q *Quotient) VerifyQuotient(a *Automaton) error {
	p := q.Part
	type key struct {
		to    int
		label string
	}
	blockEdges := func(state int) map[key]int {
		m := make(map[key]int)
		for _, e := range a.Edges {
			if e.From == state {
				m[key{p.BlockOf[e.To], e.Label}]++
			}
		}
		return m
	}
	for b, members := range p.Blocks {
		rep := blockEdges(members[0])
		for _, s := range members[1:] {
			cur := blockEdges(s)
			if len(cur) != len(rep) {
				return fmt.Errorf("verify: state %d not interchangeable with representative %d in block %d",
					s, members[0], b)
			}
			for k, n := range rep {
				if cur[k] != n {
					return fmt.Errorf("verify: state %d not interchangeable with representative %d in block %d",
						s, members[0], b)
				}
			}
		}
		if a.Outputs[members[0]] != q.Outputs[b] {
			return fmt.Errorf("verify: block %d output mismatch", b)
		}
	}
	return nil
}
