package main

import (
	"fmt"
	"sort"
	"strings"
)

// CostTable holds non-negative integer costs for the three edit operations.
// Per-label entries override the defaults. Rename of a label to itself is
// always free.
type CostTable struct {
	Ins    map[string]int
	Del    map[string]int
	Ren    map[[2]string]int
	InsDef int
	DelDef int
	RenDef int
}

// InsCost is the cost of inserting a single node with the given label.
func (c *CostTable) InsCost(label string) int {
	if v, ok := c.Ins[label]; ok {
		return v
	}
	return c.InsDef
}

// DelCost is the cost of deleting a single node with the given label. The
// deleted node's children are lifted into its place at no extra charge; the
// cost never depends on the size of the node's subtree.
func (c *CostTable) DelCost(label string) int {
	if v, ok := c.Del[label]; ok {
		return v
	}
	return c.DelDef
}

// RenCost is the cost of renaming label a to label b.
func (c *CostTable) RenCost(a, b string) int {
	if a == b {
		return 0
	}
	if v, ok := c.Ren[[2]string{a, b}]; ok {
		return v
	}
	return c.RenDef
}

// costsJSON is the wire format for CostTable. Each section maps a label (or
// "A>B" for renames) to a non-negative integer; "*" sets the default.
type costsJSON struct {
	Insert map[string]int `json:"insert"`
	Delete map[string]int `json:"delete"`
	Rename map[string]int `json:"rename"`
}

func parseCosts(cj costsJSON) (*CostTable, error) {
	ct := &CostTable{
		Ins:    map[string]int{},
		Del:    map[string]int{},
		Ren:    map[[2]string]int{},
		InsDef: 1,
		DelDef: 1,
		RenDef: 1,
	}
	fill := func(m map[string]int, def *int, into map[string]int, name string) error {
		for k, v := range m {
			if v < 0 {
				return fmt.Errorf("%s cost for %q is negative (%d)", name, k, v)
			}
			if k == "*" {
				*def = v
			} else {
				into[k] = v
			}
		}
		return nil
	}
	if err := fill(cj.Insert, &ct.InsDef, ct.Ins, "insert"); err != nil {
		return nil, err
	}
	if err := fill(cj.Delete, &ct.DelDef, ct.Del, "delete"); err != nil {
		return nil, err
	}
	for k, v := range cj.Rename {
		if v < 0 {
			return nil, fmt.Errorf("rename cost for %q is negative (%d)", k, v)
		}
		if k == "*" {
			ct.RenDef = v
			continue
		}
		parts := strings.SplitN(k, ">", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("rename key %q must have the form FROM>TO", k)
		}
		ct.Ren[[2]string{parts[0], parts[1]}] = v
	}
	return ct, nil
}

// closedCosts is the metric closure of a CostTable over a label universe.
//
// A general non-negative cost table need not be closed under composition:
// inserting a node with a "wrong" label and renaming it afterwards can be
// cheaper than inserting the intended label directly (and likewise for
// deletes and renames). The classic mapping-based dynamic program is only
// exact for the effective costs
//
//	ren'(a,b) = cheapest rename chain a -> ... -> b
//	ins'(b)   = min over x of ins(x) + ren'(x,b)
//	del'(a)   = min over y of ren'(a,y) + del(y)
//
// so the dynamic program runs on eff, and the script generator expands every
// logical operation back into the primitive chain that realizes its cost.
type closedCosts struct {
	eff    *CostTable
	renHop map[[2]string]string // next hop on a cheapest rename chain
	insAs  map[string]string    // insert b as this label, then rename
	delAs  map[string]string    // delete a after renaming to this label
}

// labelUniverse collects every label that can play a role: labels in the
// trees, labels mentioned by the cost table, and one fresh wildcard standing
// for "any other label" (all of which share the default costs).
func (c *CostTable) labelUniverse(trees ...*Node) []string {
	set := map[string]bool{}
	for _, t := range trees {
		CollectLabels(t, set)
	}
	for l := range c.Ins {
		set[l] = true
	}
	for l := range c.Del {
		set[l] = true
	}
	for p := range c.Ren {
		set[p[0]] = true
		set[p[1]] = true
	}
	wild := "\x00wildcard\x00"
	for set[wild] {
		wild += "\x00"
	}
	set[wild] = true
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// close computes the metric closure of c over the given label universe.
func (c *CostTable) close(universe []string) *closedCosts {
	n := len(universe)
	idx := make(map[string]int, n)
	for i, l := range universe {
		idx[l] = i
	}
	const inf = int(^uint(0) >> 2)
	dist := make([][]int, n)
	hop := make([][]string, n)
	for i := range dist {
		dist[i] = make([]int, n)
		hop[i] = make([]string, n)
		for j := range dist[i] {
			if i == j {
				dist[i][j] = 0
			} else {
				dist[i][j] = c.RenCost(universe[i], universe[j])
			}
			hop[i][j] = universe[j]
		}
	}
	// Floyd-Warshall; strict < keeps chains short and, with non-negative
	// weights, cycle-free.
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if dist[i][k] >= inf {
				continue
			}
			for j := 0; j < n; j++ {
				if d := dist[i][k] + dist[k][j]; d < dist[i][j] {
					dist[i][j] = d
					hop[i][j] = hop[i][k]
				}
			}
		}
	}

	cc := &closedCosts{
		eff: &CostTable{
			Ins: map[string]int{}, Del: map[string]int{}, Ren: map[[2]string]int{},
		},
		renHop: map[[2]string]string{},
		insAs:  map[string]string{},
		delAs:  map[string]string{},
	}
	for i, a := range universe {
		for j, b := range universe {
			cc.eff.Ren[[2]string{a, b}] = dist[i][j]
			cc.renHop[[2]string{a, b}] = hop[i][j]
		}
	}
	for j, b := range universe {
		best, as := c.InsCost(b), b
		for i, x := range universe {
			if d := c.InsCost(x) + dist[i][j]; d < best {
				best, as = d, x
			}
		}
		cc.eff.Ins[b] = best
		cc.insAs[b] = as
	}
	for i, a := range universe {
		best, as := c.DelCost(a), a
		for j, y := range universe {
			if d := dist[i][j] + c.DelCost(y); d < best {
				best, as = d, y
			}
		}
		cc.eff.Del[a] = best
		cc.delAs[a] = as
	}
	// Defaults are only a fallback for labels outside the universe; keep
	// them at the cheapest effective value found.
	cc.eff.InsDef, cc.eff.DelDef, cc.eff.RenDef = c.InsDef, c.DelDef, c.RenDef
	for _, b := range universe {
		cc.eff.InsDef = min(cc.eff.InsDef, cc.eff.Ins[b])
		cc.eff.DelDef = min(cc.eff.DelDef, cc.eff.Del[b])
	}
	return cc
}

// renChain returns a cheapest label chain [a ... b]; a==b gives [a]. The
// walk is bounded by the universe size, so zero-cost entries cannot make it
// loop.
func (cc *closedCosts) renChain(a, b string) []string {
	chain := []string{a}
	for cur := a; cur != b; {
		next, ok := cc.renHop[[2]string{cur, b}]
		if !ok {
			panic(fmt.Sprintf("cost closure: no rename chain %q -> %q", cur, b))
		}
		chain = append(chain, next)
		cur = next
		if len(chain) > len(cc.insAs)+2 {
			panic("cost closure: rename chain does not terminate")
		}
	}
	return chain
}

// insChain returns the label chain realizing the effective insert of b:
// insert a node with the first label, then rename along the chain.
func (cc *closedCosts) insChain(b string) []string {
	return cc.renChain(cc.insAs[b], b)
}

// delChain returns the label chain realizing the effective delete of a:
// rename along the chain, then delete the final label.
func (cc *closedCosts) delChain(a string) []string {
	return cc.renChain(a, cc.delAs[a])
}
