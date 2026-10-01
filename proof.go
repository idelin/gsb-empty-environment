package reasoner

import (
	"fmt"
	"sort"
	"strings"
)

// Proof is a proof tree node. Base facts carry no rule; derived facts carry
// the rule that produced them and one sub-proof per premise.
type Proof struct {
	Fact     Fact
	Rule     *Rule // nil for asserted base facts
	Premises []*Proof
}

// Asserted reports whether this node is a base fact.
func (p *Proof) Asserted() bool { return p.Rule == nil }

// Explain returns a minimal proof tree for f: among all derivations of each
// fact it picks the one with the shallowest depth, breaking ties by the
// smallest total number of nodes. Asserted base facts are always preferred.
func (e *Engine) Explain(f Fact) (*Proof, error) {
	if !e.Known(f) {
		return nil, fmt.Errorf("cannot explain %q: fact is not known", f)
	}
	return e.bestProof(f, make(map[Fact]*Proof), make(map[Fact]bool)), nil
}

func (e *Engine) bestProof(f Fact, memo map[Fact]*Proof, visiting map[Fact]bool) *Proof {
	if p, ok := memo[f]; ok {
		return p
	}
	st := e.facts[f]
	var best *Proof
	if st.asserted {
		best = &Proof{Fact: f}
	}
	if !visiting[f] {
		visiting[f] = true
		for _, r := range st.derivations {
			cand := &Proof{Fact: f, Rule: r}
			ok := true
			for _, prem := range r.Premises {
				sub := e.bestProof(prem, memo, visiting)
				if sub == nil { // cyclic derivation, no finite proof this way
					ok = false
					break
				}
				cand.Premises = append(cand.Premises, sub)
			}
			if ok && (best == nil || lessProof(cand, best)) {
				best = cand
			}
		}
		delete(visiting, f)
	}
	memo[f] = best
	return best
}

// lessProof orders proofs by (depth, size): shallower first, then smaller.
func lessProof(a, b *Proof) bool {
	da, db := a.Depth(), b.Depth()
	if da != db {
		return da < db
	}
	return a.Size() < b.Size()
}

// Depth is the length of the longest evidence chain in the proof.
func (p *Proof) Depth() int {
	if p.Rule == nil {
		return 0
	}
	max := 0
	for _, sub := range p.Premises {
		if d := sub.Depth(); d > max {
			max = d
		}
	}
	return max + 1
}

// Size is the total number of nodes in the proof tree.
func (p *Proof) Size() int {
	n := 1
	for _, sub := range p.Premises {
		n += sub.Size()
	}
	return n
}

// BaseFacts returns the deduplicated, sorted set of base facts the proof
// rests upon.
func (p *Proof) BaseFacts() []Fact {
	seen := make(map[Fact]bool)
	var collect func(*Proof)
	collect = func(n *Proof) {
		if n.Rule == nil {
			seen[n.Fact] = true
			return
		}
		for _, sub := range n.Premises {
			collect(sub)
		}
	}
	collect(p)
	out := make([]Fact, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Steps flattens the proof into an ordered evidence chain: every step is one
// rule application, with premises always derived before their conclusion.
func (p *Proof) Steps() []string {
	done := make(map[Fact]bool)
	var steps []string
	var walk func(*Proof)
	walk = func(n *Proof) {
		if done[n.Fact] {
			return
		}
		done[n.Fact] = true
		if n.Rule == nil {
			steps = append(steps, fmt.Sprintf("fact %s (asserted)", n.Fact))
			return
		}
		for _, sub := range n.Premises {
			walk(sub)
		}
		steps = append(steps, fmt.Sprintf("fact %s (by %s)", n.Fact, n.Rule))
	}
	walk(p)
	return steps
}

// String renders the proof as an indented tree.
func (p *Proof) String() string {
	var b strings.Builder
	var render func(*Proof, int)
	render = func(n *Proof, indent int) {
		pad := strings.Repeat("  ", indent)
		if n.Rule == nil {
			fmt.Fprintf(&b, "%s%s [asserted]\n", pad, n.Fact)
			return
		}
		fmt.Fprintf(&b, "%s%s [by %s]\n", pad, n.Fact, n.Rule.Name)
		for _, sub := range n.Premises {
			render(sub, indent+1)
		}
	}
	render(p, 0)
	return b.String()
}
