package main

import (
	"sort"
	"strings"
)

// normCosts is the effective cost model used by the DP. Arbitrary
// non-negative cost tables need not satisfy the triangle inequality, so a
// script could rename a->x->b more cheaply than a->b, or insert label a and
// rename it to b more cheaply than inserting b directly. We close the cost
// table under such chains (Floyd-Warshall) so that the tree edit DP over
// these effective costs equals the true minimum script cost. The concrete
// op sequences realizing each effective cost are kept for script emission.
type normCosts struct {
	raw     *CostTable
	fresh   string               // a concrete label standing in for "any unlisted label"
	renEff  map[[2]string]int    // effective rename cost between tree labels
	renNext map[[2]string]string // next hop label on a cheapest rename chain
	insEff  map[string]int       // effective cost of materializing a node with this label
	insVia  map[string]string    // label to actually insert before renaming
	delEff  map[string]int       // effective cost of eliminating a node with this label
	delVia  map[string]string    // label to rename to before deleting
}

func (nc *normCosts) Ins(l string) int { return nc.insEff[l] }
func (nc *normCosts) Del(l string) int { return nc.delEff[l] }
func (nc *normCosts) Ren(a, b string) int {
	if a == b {
		return 0
	}
	return nc.renEff[[2]string{a, b}]
}

// renameChain returns the sequence of labels to rename through to get from
// a to b at effective cost (excluding a, ending at b). Empty if a == b.
func (nc *normCosts) renameChain(a, b string) []string {
	var chain []string
	cur := a
	for cur != b {
		nxt, ok := nc.renNext[[2]string{cur, b}]
		if !ok || len(chain) > len(nc.renEff) { // safety: never loop
			return []string{b}
		}
		chain = append(chain, nxt)
		cur = nxt
	}
	return chain
}

// labelUniverse returns every label relevant to the problem: labels in both
// trees plus every label mentioned by the cost table.
func labelUniverse(t1, t2 *Tree, c *CostTable) map[string]bool {
	u := map[string]bool{}
	collectLabels(t1, u)
	collectLabels(t2, u)
	for l := range c.InsertLabel {
		u[l] = true
	}
	for l := range c.DeleteLabel {
		u[l] = true
	}
	for k := range c.RenamePair {
		parts := strings.SplitN(k, ">", 2)
		if len(parts) == 2 {
			u[parts[0]] = true
			u[parts[1]] = true
		}
	}
	return u
}

func freshLabel(taken map[string]bool) string {
	f := "\x00fresh"
	for taken[f] {
		f += "\x00"
	}
	return f
}

// normalizeCosts closes the cost table under rename chains.
func normalizeCosts(c *CostTable, t1, t2 *Tree) *normCosts {
	u := labelUniverse(t1, t2, c)
	fresh := freshLabel(u)
	u[fresh] = true

	labels := make([]string, 0, len(u))
	for l := range u {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	idx := map[string]int{}
	for i, l := range labels {
		idx[l] = i
	}
	n := len(labels)

	d := make([][]int, n)
	nxt := make([][]string, n)
	for i := range d {
		d[i] = make([]int, n)
		nxt[i] = make([]string, n)
		for j := range d[i] {
			if i == j {
				d[i][j] = 0
			} else {
				d[i][j] = c.Ren(labels[i], labels[j])
			}
			nxt[i][j] = labels[j]
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if v := d[i][k] + d[k][j]; v < d[i][j] {
					d[i][j] = v
					nxt[i][j] = nxt[i][k]
				}
			}
		}
	}

	nc := &normCosts{
		raw:     c,
		fresh:   fresh,
		renEff:  map[[2]string]int{},
		renNext: map[[2]string]string{},
		insEff:  map[string]int{},
		insVia:  map[string]string{},
		delEff:  map[string]int{},
		delVia:  map[string]string{},
	}
	for i, a := range labels {
		for j, b := range labels {
			nc.renEff[[2]string{a, b}] = d[i][j]
			nc.renNext[[2]string{a, b}] = nxt[i][j]
		}
	}
	// Effective insert: insert some label a, then rename chain a ~> target.
	for _, tgt := range labels {
		best, via := c.Ins(tgt), tgt
		for _, a := range labels {
			if v := c.Ins(a) + d[idx[a]][idx[tgt]]; v < best {
				best, via = v, a
			}
		}
		nc.insEff[tgt] = best
		nc.insVia[tgt] = via
	}
	// Effective delete: rename chain to some label b, then delete b.
	for _, src := range labels {
		best, via := c.Del(src), src
		for _, b := range labels {
			if v := d[idx[src]][idx[b]] + c.Del(b); v < best {
				best, via = v, b
			}
		}
		nc.delEff[src] = best
		nc.delVia[src] = via
	}
	return nc
}
