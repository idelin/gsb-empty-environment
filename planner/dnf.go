package planner

import "sort"

// Term is one conjunctive satisfaction path: evidence that must be
// collected (Require) and evidence that must be absent (Forbid).
type Term struct {
	Require map[string]bool
	Forbid  map[string]bool
}

// Key uniquely identifies a term for deduplication.
func (t Term) Key() string {
	return sortedKeys(t.Require) + "|" + sortedKeys(t.Forbid)
}

func sortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k
	}
	return out
}

// toNNF pushes negations down to literals (De Morgan).
func toNNF(n *Node, neg bool) *Node {
	switch n.Kind {
	case KindEvidence:
		if neg {
			return &Node{Kind: KindNot, Children: []*Node{{Kind: KindEvidence, ID: n.ID}}}
		}
		return &Node{Kind: KindEvidence, ID: n.ID}
	case KindNot:
		return toNNF(n.Children[0], !neg)
	case KindAnd:
		kind := KindAnd
		if neg {
			kind = KindOr
		}
		out := &Node{Kind: kind}
		for _, ch := range n.Children {
			out.Children = append(out.Children, toNNF(ch, neg))
		}
		return out
	case KindOr:
		kind := KindOr
		if neg {
			kind = KindAnd
		}
		out := &Node{Kind: kind}
		for _, ch := range n.Children {
			out.Children = append(out.Children, toNNF(ch, neg))
		}
		return out
	}
	return n
}

// Terms enumerates the DNF satisfaction paths of the expression.
// Each returned term is one way to make the expression true.
// Contradictory conjunctions (E and NOT E) are dropped.
func Terms(root *Node) []Term {
	return dnf(toNNF(root, false))
}

func dnf(n *Node) []Term {
	switch n.Kind {
	case KindEvidence:
		return []Term{{Require: map[string]bool{n.ID: true}, Forbid: map[string]bool{}}}
	case KindNot:
		// After NNF, NOT only wraps a literal.
		lit := n.Children[0]
		return []Term{{Require: map[string]bool{}, Forbid: map[string]bool{lit.ID: true}}}
	case KindOr:
		var out []Term
		for _, ch := range n.Children {
			out = append(out, dnf(ch)...)
		}
		return out
	case KindAnd:
		acc := []Term{{Require: map[string]bool{}, Forbid: map[string]bool{}}}
		for _, ch := range n.Children {
			sub := dnf(ch)
			var next []Term
			for _, a := range acc {
				for _, b := range sub {
					if merged, ok := mergeTerms(a, b); ok {
						next = append(next, merged)
					}
				}
			}
			acc = next
			if len(acc) == 0 {
				return nil
			}
		}
		return acc
	}
	return nil
}

func mergeTerms(a, b Term) (Term, bool) {
	out := Term{Require: make(map[string]bool), Forbid: make(map[string]bool)}
	for id := range a.Require {
		out.Require[id] = true
	}
	for id := range b.Require {
		out.Require[id] = true
	}
	for id := range a.Forbid {
		out.Forbid[id] = true
	}
	for id := range b.Forbid {
		out.Forbid[id] = true
	}
	for id := range out.Require {
		if out.Forbid[id] {
			return Term{}, false // contradictory: E and NOT E
		}
	}
	return out, true
}

// DedupeTerms removes duplicate terms.
func DedupeTerms(terms []Term) []Term {
	seen := make(map[string]bool)
	var out []Term
	for _, t := range terms {
		k := t.Key()
		if !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	return out
}
