package cteopt

import "sort"

// Decision records the optimizer's verdict for one repeated subexpression.
type Decision struct {
	Label           string
	Refs            int
	SubexprCost     float64 // cost of computing the subexpression once
	InlineCost      float64 // Refs * SubexprCost
	MaterializeCost float64 // compute once + buffer writes + buffered reads
	Materialized    bool
}

// Optimizer rewrites a plan DAG, replacing profitable repeated
// subexpressions with a single shared Materialize node.
type Optimizer struct {
	Tables    map[string][]Row
	Decisions []Decision
}

// Optimize returns a rewritten plan. The input is not mutated.
func (o *Optimizer) Optimize(root *Node) *Node {
	o.Decisions = nil
	Annotate(root, o.Tables)

	nodes := collect(root)
	fpMemo := map[*Node]string{}
	fpOf := map[*Node]string{}
	groups := map[string][]*Node{}
	for _, n := range nodes {
		fp := fingerprint(n, fpMemo)
		fpOf[n] = fp
		groups[fp] = append(groups[fp], n)
	}

	// Count references: one per parent edge, plus one for the root.
	refs := map[string]int{fpOf[root]: 1}
	for _, n := range nodes {
		for _, c := range n.Children {
			refs[fpOf[c]]++
		}
	}

	type candidate struct {
		fp      string
		members []*Node
		size    int
	}
	var cands []candidate
	for fp, members := range groups {
		if refs[fp] >= 2 && members[0].Op != OpMaterialize {
			cands = append(cands, candidate{fp, members, subtreeSize(members[0])})
		}
	}
	// Largest subtrees first: materializing an outer repeat covers the
	// inner repeats it contains.
	sort.Slice(cands, func(i, j int) bool { return cands[i].size > cands[j].size })

	covered := map[*Node]bool{}
	repl := map[*Node]*Node{}
	for _, c := range cands {
		if allCovered(c.members, covered) {
			continue
		}
		canonical := c.members[0]
		r := refs[c.fp]
		rows := canonical.EstRows
		cost := canonical.Cost
		inlineTotal := float64(r) * cost
		matTotal := cost + rows*MatWriteRow + float64(r)*rows*MatReadRow
		materialize := cost >= MinCostToMaterialize && matTotal < inlineTotal
		o.Decisions = append(o.Decisions, Decision{
			Label:           canonical.Label,
			Refs:            r,
			SubexprCost:     cost,
			InlineCost:      inlineTotal,
			MaterializeCost: matTotal,
			Materialized:    materialize,
		})
		if !materialize {
			continue
		}
		mat := &Node{
			Op:       OpMaterialize,
			Label:    canonical.Label,
			Children: []*Node{canonical},
			EstRows:  rows,
			Cost:     cost + rows*MatWriteRow,
		}
		for _, m := range c.members {
			repl[m] = mat
			markCovered(m, covered)
		}
	}

	return rewrite(root, repl, map[*Node]*Node{})
}

func subtreeSize(n *Node) int {
	count := 0
	seen := map[*Node]bool{}
	var walk func(*Node)
	walk = func(x *Node) {
		if seen[x] {
			return
		}
		seen[x] = true
		count++
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return count
}

func allCovered(members []*Node, covered map[*Node]bool) bool {
	for _, m := range members {
		ok := true
		var walk func(*Node)
		walk = func(x *Node) {
			if !covered[x] {
				ok = false
			}
			for _, c := range x.Children {
				walk(c)
			}
		}
		walk(m)
		if !ok {
			return false
		}
	}
	return true
}

func markCovered(n *Node, covered map[*Node]bool) {
	covered[n] = true
	for _, c := range n.Children {
		markCovered(c, covered)
	}
}

func rewrite(n *Node, repl, memo map[*Node]*Node) *Node {
	if r, ok := repl[n]; ok {
		return r
	}
	if m, ok := memo[n]; ok {
		return m
	}
	cpy := *n
	cpy.Children = make([]*Node, len(n.Children))
	for i, c := range n.Children {
		cpy.Children[i] = rewrite(c, repl, memo)
	}
	memo[n] = &cpy
	return &cpy
}
