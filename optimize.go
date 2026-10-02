package cteopt

import (
	"fmt"
	"strings"
)

// Optimize detects common subexpressions in the plan DAG and, for each one,
// decides cost-based whether to keep it inlined (recomputed per reference)
// or to compute it once into a Materialize spool referenced by CTERefs.
func Optimize(root *Node, st Stats) *Node {
	Estimate(root, st)
	refs, groups := analyze(root)

	decided := map[string]*Node{} // structural key -> Materialize node
	seq := 0
	for k, members := range groups {
		total := 0
		for _, m := range members {
			total += refs[m]
		}
		if total < 2 {
			continue
		}
		rep := members[0]
		if rep.Op == Materialize || rep.Op == CTERef {
			continue
		}
		inline := float64(total) * rep.Cost
		mat := rep.Cost + rep.Rows*writeRowCost + materializeFix + float64(total)*rep.Rows*readRowCost
		if mat < inline {
			seq++
			decided[k] = &Node{
				Op:       Materialize,
				Name:     fmt.Sprintf("cte_%d", seq),
				Rows:     rep.Rows,
				Cost:     rep.Cost,
				Children: []*Node{rep},
			}
		}
	}
	if len(decided) == 0 {
		return root
	}
	out := rebuild(root, decided, map[*Node]string{}, map[*Node]*Node{})
	Estimate(out, st)
	return out
}

// analyze counts parent references per node and groups structurally
// identical subtrees (shared DAG pointers and equal-but-distinct subtrees).
func analyze(root *Node) (refs map[*Node]int, groups map[string][]*Node) {
	refs = map[*Node]int{}
	groups = map[string][]*Node{}
	visited := map[*Node]bool{}
	km := map[*Node]string{}
	var walk func(n *Node)
	walk = func(n *Node) {
		refs[n]++
		if visited[n] {
			return
		}
		visited[n] = true
		k := key(n, km)
		groups[k] = append(groups[k], n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return refs, groups
}

// key is the structural identity of a subtree.
func key(n *Node, memo map[*Node]string) string {
	if s, ok := memo[n]; ok {
		return s
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s|%s|%s|%s|%s|", n.Op, n.Table, n.Alias, n.Pred, n.JoinKey, strings.Join(n.Cols, ","))
	for _, c := range n.Children {
		b.WriteString(key(c, memo))
		b.WriteByte(';')
	}
	s := b.String()
	memo[n] = s
	return s
}

// rebuild copies the plan, replacing every subexpression whose key was
// decided with a CTERef pointing at the shared Materialize spool.
// Note: the spool keeps the original subtree, so a decided expression
// nested inside another decided one stays inline within the spool.
func rebuild(n *Node, decided map[string]*Node, km map[*Node]string, rm map[*Node]*Node) *Node {
	if m, ok := decided[key(n, km)]; ok {
		return &Node{Op: CTERef, Name: m.Name, Src: m, Rows: n.Rows}
	}
	if r, ok := rm[n]; ok {
		return r
	}
	cp := *n
	cp.Children = nil
	for _, c := range n.Children {
		cp.Children = append(cp.Children, rebuild(c, decided, km, rm))
	}
	rm[n] = &cp
	return &cp
}
