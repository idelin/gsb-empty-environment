package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
)

func mustTree(t *testing.T, s string) *Tree {
	t.Helper()
	tr, err := ParseTree([]byte(s))
	if err != nil {
		t.Fatalf("bad tree %s: %v", s, err)
	}
	return tr
}

// solve runs the full pipeline and returns distance, ops; it also checks the
// internal script simulation and the independent applier.
func solve(t *testing.T, src, tgt *Tree, c *CostTable) (int, []Op) {
	t.Helper()
	nc := normalizeCosts(c, src, tgt)
	dist, mapping := treeEditDistance(src, tgt, nc)
	ops, err := buildScript(src, tgt, mapping, nc)
	if err != nil {
		t.Fatalf("buildScript: %v", err)
	}
	res, cost, err := ApplyScript(src, ops, c)
	if err != nil {
		t.Fatalf("ApplyScript: %v", err)
	}
	if cost != dist {
		t.Fatalf("applier cost %d != DP distance %d\nops=%v", cost, dist, ops)
	}
	if !Equal(res, tgt) {
		t.Fatalf("applier result != target\nops=%v", ops)
	}
	return dist, ops
}

func TestKnownDistances(t *testing.T) {
	unit := DefaultCosts()
	cases := []struct {
		src, tgt string
		costs    *CostTable
		want     int
	}{
		{`{"label":"a"}`, `{"label":"a"}`, unit, 0},
		{`{"label":"a"}`, `{"label":"b"}`, unit, 1},
		{`{"label":"a","children":[{"label":"b"},{"label":"c"}]}`,
			`{"label":"a","children":[{"label":"c"},{"label":"b"}]}`, unit, 2},
		// insert x adopting a: single insert
		{`{"label":"r","children":[{"label":"a"}]}`,
			`{"label":"r","children":[{"label":"x","children":[{"label":"a"}]}]}`, unit, 1},
		// insert x above b adopting the whole subtree
		{`{"label":"a","children":[{"label":"b","children":[{"label":"c"}]}]}`,
			`{"label":"a","children":[{"label":"x","children":[{"label":"b","children":[{"label":"c"}]}]}]}`, unit, 1},
		// del a + rename r->a = 2
		{`{"label":"r","children":[{"label":"a"},{"label":"b"}]}`,
			`{"label":"a","children":[{"label":"b"}]}`, unit, 2},
		// rename a->x, then insert a = 2
		{`{"label":"a","children":[{"label":"b"}]}`,
			`{"label":"x","children":[{"label":"a"},{"label":"b"}]}`, unit, 2},
		// chained rename beats direct rename: a->b->c = 2 < 5
		{`{"label":"a"}`, `{"label":"c"}`, &CostTable{
			Insert: 9, Delete: 9, Rename: 5,
			RenamePair: map[string]int{"a>b": 1, "b>c": 1},
		}, 2},
		// insert cheap label then rename beats expensive insert
		{`{"label":"r"}`, `{"label":"r","children":[{"label":"c"}]}`, &CostTable{
			Insert: 1, Delete: 1, Rename: 1,
			InsertLabel: map[string]int{"c": 100},
		}, 2},
		// rename then delete beats expensive delete
		{`{"label":"r","children":[{"label":"a"}]}`, `{"label":"r"}`, &CostTable{
			Insert: 1, Delete: 1, Rename: 1,
			DeleteLabel: map[string]int{"a": 100},
		}, 2},
		// all-zero costs: everything is free
		{`{"label":"a","children":[{"label":"b"}]}`,
			`{"label":"x","children":[{"label":"y","children":[{"label":"z"}]}]}`,
			&CostTable{Insert: 0, Delete: 0, Rename: 0}, 0},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			if err := tc.costs.Validate(); err != nil {
				t.Fatal(err)
			}
			src, tgt := mustTree(t, tc.src), mustTree(t, tc.tgt)
			dist, _ := solve(t, src, tgt, tc.costs)
			if dist != tc.want {
				t.Fatalf("distance = %d, want %d", dist, tc.want)
			}
		})
	}
}

// TestDeleteChargesSingleNode pins the applier semantics: deleting a node
// lifts its children and costs Del(label) once, never the whole subtree.
func TestDeleteChargesSingleNode(t *testing.T) {
	c := &CostTable{
		Insert: 1, Delete: 1, Rename: 1,
		DeleteLabel: map[string]int{"b": 5, "c": 1, "d": 1},
	}
	src := mustTree(t, `{"label":"a","children":[{"label":"b","children":[{"label":"c"},{"label":"d"}]}]}`)
	// postorder ids: c=1, d=2, b=3, a=4
	ops := []Op{{Op: "delete", Node: 3}}
	res, cost, err := ApplyScript(src, ops, c)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 5 {
		t.Fatalf("delete of b cost %d, want 5 (single node, children lifted)", cost)
	}
	want := mustTree(t, `{"label":"a","children":[{"label":"c"},{"label":"d"}]}`)
	if !Equal(res, want) {
		got, _ := json.Marshal(res)
		t.Fatalf("result %s, want children lifted in place", got)
	}
}

// TestInsertAdoptsChildren checks the inverse-of-delete insert semantics.
func TestInsertAdoptsChildren(t *testing.T) {
	c := DefaultCosts()
	src := mustTree(t, `{"label":"r","children":[{"label":"a"}]}`)
	tgt := mustTree(t, `{"label":"r","children":[{"label":"x","children":[{"label":"a"}]}]}`)
	dist, ops := solve(t, src, tgt, c)
	if dist != 1 || len(ops) != 1 {
		t.Fatalf("dist=%d ops=%v, want a single insert", dist, ops)
	}
	op := ops[0]
	if op.Op != "insert" || op.Adopt != 1 || op.Label != "x" || op.Parent != 2 || op.Index != 0 {
		t.Fatalf("unexpected op %+v, want insert x under r(2) at 0 adopting 1", op)
	}
}

// TestZeroCostNoLoop ensures zero-cost tables cannot make the exhaustive
// search loop and still agree with the DP.
func TestZeroCostNoLoop(t *testing.T) {
	c := &CostTable{Insert: 0, Delete: 0, Rename: 0}
	src := mustTree(t, `{"label":"a","children":[{"label":"b"},{"label":"c"}]}`)
	tgt := mustTree(t, `{"label":"x","children":[{"label":"y","children":[{"label":"z"}]}]}`)
	dist, _ := solve(t, src, tgt, c)
	if dist != 0 {
		t.Fatalf("zero-cost distance = %d, want 0", dist)
	}
	ed, states, ok := ExhaustiveDistance(src, tgt, c, 500000)
	if !ok {
		t.Fatalf("exhaustive inconclusive after %d states", states)
	}
	if ed != 0 {
		t.Fatalf("exhaustive distance = %d, want 0", ed)
	}
}

func randTree(r *rand.Rand, labels []string, maxNodes int) *Tree {
	nodes := 1 + r.Intn(maxNodes)
	root := &Tree{Label: labels[r.Intn(len(labels))]}
	all := []*Tree{root}
	for i := 1; i < nodes; i++ {
		p := all[r.Intn(len(all))]
		nw := &Tree{Label: labels[r.Intn(len(labels))]}
		p.Children = append(p.Children, nw)
		all = append(all, nw)
	}
	return root
}

func randCosts(r *rand.Rand, labels []string) *CostTable {
	c := &CostTable{
		Insert: r.Intn(4), Delete: r.Intn(4), Rename: r.Intn(4),
	}
	if r.Intn(2) == 0 {
		c.InsertLabel = map[string]int{}
		c.DeleteLabel = map[string]int{}
		for _, l := range labels {
			if r.Intn(2) == 0 {
				c.InsertLabel[l] = r.Intn(4)
			}
			if r.Intn(2) == 0 {
				c.DeleteLabel[l] = r.Intn(4)
			}
		}
	}
	if r.Intn(2) == 0 {
		c.RenamePair = map[string]int{}
		for _, a := range labels {
			for _, b := range labels {
				if a != b && r.Intn(3) == 0 {
					c.RenamePair[a+">"+b] = r.Intn(4)
				}
			}
		}
	}
	return c
}

// TestExhaustiveRootOps exercises root deletion (forest intermediate states)
// and root insertion in the exhaustive checker against the DP.
func TestExhaustiveRootOps(t *testing.T) {
	c := DefaultCosts()
	pairs := [][2]string{
		{`{"label":"a","children":[{"label":"b"}]}`, `{"label":"x","children":[{"label":"a"},{"label":"b"}]}`},
		{`{"label":"x","children":[{"label":"a"},{"label":"b"}]}`, `{"label":"a","children":[{"label":"b"}]}`},
		{`{"label":"a"}`, `{"label":"x","children":[{"label":"a"}]}`},
	}
	for i, pr := range pairs {
		src, tgt := mustTree(t, pr[0]), mustTree(t, pr[1])
		dist, _ := solve(t, src, tgt, c)
		ed, _, ok := ExhaustiveDistance(src, tgt, c, 1000000)
		if !ok || ed != dist {
			t.Fatalf("pair %d: DP=%d exhaustive=%d ok=%v", i, dist, ed, ok)
		}
	}
}
