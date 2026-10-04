package main

import "sort"

// Fact is an initial fact. Cost is charged once when the fact is actually
// used in a proof (default 0).
type Fact struct {
	Name string  `json:"name"`
	Cost float64 `json:"cost"`
}

// Hyperedge derives Target once every premise in Premises is available.
type Hyperedge struct {
	ID       string   `json:"id"`
	Premises []string `json:"from"`
	Target   string   `json:"to"`
	Cost     float64  `json:"cost"`
}

// Problem is a hypergraph derivation instance.
type Problem struct {
	Facts []Fact
	Edges []Hyperedge
}

// augmented returns the internal edge list: every initial fact becomes a
// zero-premise hyperedge carrying the fact cost, followed by the real
// edges. This makes the whole solver uniform: a proof is a set of
// hyperedges, its cost is the sum of the distinct edge costs, and shared
// subproofs are charged once because each edge appears at most once.
func (p *Problem) augmented() []Hyperedge {
	aug := make([]Hyperedge, 0, len(p.Facts)+len(p.Edges))
	for i, f := range p.Facts {
		aug = append(aug, Hyperedge{
			ID:       idOr(f.Name, "fact", i),
			Premises: nil,
			Target:   f.Name,
			Cost:     f.Cost,
		})
	}
	for i, e := range p.Edges {
		cp := e
		cp.ID = idOr(e.ID, "e", i)
		cp.Premises = dedup(e.Premises)
		aug = append(aug, cp)
	}
	return aug
}

func idOr(id string, prefix string, i int) string {
	if id != "" {
		return id
	}
	return prefix + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// closure computes the forward closure of the empty set under edges:
// the set of nodes derivable when only the given edges may fire.
// Purely cyclic self-support ("A needs B, B needs A" with no base) never
// enters the closure, because no edge of the cycle can fire first.
func closure(edges []Hyperedge) map[string]bool {
	derived := map[string]bool{}
	remaining := make([]Hyperedge, len(edges))
	copy(remaining, edges)
	for progress := true; progress; {
		progress = false
		next := remaining[:0]
		for _, e := range remaining {
			ready := true
			for _, pre := range e.Premises {
				if !derived[pre] {
					ready = false
					break
				}
			}
			if ready {
				if !derived[e.Target] {
					derived[e.Target] = true
					progress = true
				}
			} else {
				next = append(next, e)
			}
		}
		remaining = next
	}
	return derived
}

// Derivable returns the set of nodes derivable from the initial facts.
func (p *Problem) Derivable() map[string]bool {
	return closure(p.augmented())
}

// liveEdges drops edges that can never fire because some premise is not
// producible by any edge (nor a fact). Returns the live edges and the
// sorted list of producible node names.
func liveEdges(aug []Hyperedge) ([]Hyperedge, []string) {
	producible := map[string]bool{}
	for _, e := range aug {
		producible[e.Target] = true
	}
	live := aug[:0]
	for _, e := range aug {
		ok := true
		for _, pre := range e.Premises {
			if !producible[pre] {
				ok = false
				break
			}
		}
		if ok {
			live = append(live, e)
		}
	}
	names := make([]string, 0, len(producible))
	for n := range producible {
		names = append(names, n)
	}
	sort.Strings(names)
	return live, names
}
