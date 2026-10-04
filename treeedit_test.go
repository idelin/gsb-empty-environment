package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// parseTree parses a compact notation like a(b(c),d) for tests.
func parseTree(s string) *Node {
	p := &treeParser{s: s}
	n := p.node()
	if p.i != len(p.s) {
		panic("trailing input in " + s)
	}
	return n
}

type treeParser struct {
	s string
	i int
}

func (p *treeParser) node() *Node {
	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune("(),", rune(p.s[p.i])) {
		p.i++
	}
	if p.i == start {
		panic("expected label in " + p.s)
	}
	n := &Node{Label: p.s[start:p.i]}
	if p.i < len(p.s) && p.s[p.i] == '(' {
		p.i++
		for {
			n.Children = append(n.Children, p.node())
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			break
		}
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			panic("missing ) in " + p.s)
		}
		p.i++
	}
	return n
}

func unitCosts() *CostTable {
	return &CostTable{
		Ins: map[string]int{}, Del: map[string]int{}, Ren: map[[2]string]int{},
		InsDef: 1, DelDef: 1, RenDef: 1,
	}
}

func solve(t *testing.T, src, tgt *Node, c *CostTable) (int, []Op, *rawMapping) {
	t.Helper()
	dist, ops, m, err := solveAll(src, tgt, c)
	if err != nil {
		t.Fatalf("solveAll: %v", err)
	}
	if err := verifyScript(src, tgt, ops, dist, c); err != nil {
		t.Fatalf("verifyScript: %v", err)
	}
	return dist, ops, m
}

func TestDistanceExamples(t *testing.T) {
	cases := []struct {
		src, tgt string
		want     int
	}{
		{"a", "a", 0},
		{"a", "b", 1},
		{"a(b,c)", "a(b,d)", 1},
		{"a(b,c)", "a(b)", 1},
		{"a(b)", "a(b,c)", 1},
		{"a(b,c)", "a(c,b)", 2},
		{"a(b(c,d))", "a(c,d)", 1}, // delete b: children lift into its place
		{"a(b(c,d))", "a", 3},      // b, c, d each deleted individually
		{"a(b,c)", "x(y,z,w)", 4},  // 3 renames + 1 insert
		{"a(b(c))", "a(b,c)", 2},   // insert c + delete old c (no move op)
		{"a(b,c)", "d", 3},         // rename root + delete both children
		{"f(d(a,c(b)),e)", "f(c,d(a,b),e)", 2},
	}
	for _, tc := range cases {
		t.Run(tc.src+"->"+tc.tgt, func(t *testing.T) {
			src, tgt := parseTree(tc.src), parseTree(tc.tgt)
			dist, _, _ := solve(t, src, tgt, unitCosts())
			if dist != tc.want {
				t.Fatalf("distance = %d, want %d", dist, tc.want)
			}
			bd, ok := bruteForceDistance(src, tgt, unitCosts(), 300000)
			if !ok {
				t.Fatalf("brute force inconclusive for %s -> %s", tc.src, tc.tgt)
			}
			if bd != dist {
				t.Fatalf("brute force = %d, dp = %d", bd, dist)
			}
		})
	}
}

// A delete must be charged as a single node removal even when the node has a
// large subtree: the children are lifted, not deleted.
func TestDeleteChargedAsSingleNodeNotSubtree(t *testing.T) {
	c := unitCosts()
	c.DelDef = 5

	dist, ops, _ := solve(t, parseTree("a(b(c,d))"), parseTree("a(c,d)"), c)
	if dist != 5 {
		t.Fatalf("distance = %d, want 5 (one single-node delete)", dist)
	}
	deletes := 0
	for _, op := range ops {
		if op.Kind == "delete" {
			deletes++
			if op.Cost != 5 {
				t.Fatalf("delete op cost = %d, want single-node cost 5", op.Cost)
			}
		}
	}
	if deletes != 1 {
		t.Fatalf("got %d delete ops, want 1", deletes)
	}

	// Deleting the whole subtree still costs one delete per node, because
	// lifted children must be deleted individually.
	dist2, ops2, _ := solve(t, parseTree("a(b(c,d))"), parseTree("a"), c)
	if dist2 != 15 {
		t.Fatalf("distance = %d, want 15 (three single-node deletes)", dist2)
	}
	deletes2 := 0
	for _, op := range ops2 {
		if op.Kind == "delete" {
			deletes2++
		}
	}
	if deletes2 != 3 {
		t.Fatalf("got %d delete ops, want 3", deletes2)
	}
}

// Zero-cost operations must not send the search into a loop.
func TestZeroCostsTerminate(t *testing.T) {
	zero := &CostTable{
		Ins: map[string]int{}, Del: map[string]int{}, Ren: map[[2]string]int{},
		InsDef: 0, DelDef: 0, RenDef: 0,
	}
	src, tgt := parseTree("a(b(c),d(e,f))"), parseTree("x(y,z(w))")
	dist, ops, _ := solve(t, src, tgt, zero)
	if dist != 0 {
		t.Fatalf("distance = %d, want 0", dist)
	}
	if len(ops) == 0 {
		t.Fatal("expected a non-empty script even at distance 0")
	}

	// The exhaustive search must also terminate under all-zero costs; keep
	// the trees tiny so the dist-0 state cloud stays small.
	zsrc, ztgt := parseTree("a(b)"), parseTree("x(y,z)")
	bd, ok := bruteForceDistance(zsrc, ztgt, zero, 300000)
	if !ok {
		t.Fatal("brute force did not terminate within the state cap")
	}
	if bd != 0 {
		t.Fatalf("brute force = %d, want 0", bd)
	}
	solve(t, zsrc, ztgt, zero)

	// Zero-cost renames of specific pairs only.
	c := unitCosts()
	c.Ren[[2]string{"a", "x"}] = 0
	c.Ren[[2]string{"b", "y"}] = 0
	dist, _, _ = solve(t, parseTree("a(b)"), parseTree("x(y)"), c)
	if dist != 0 {
		t.Fatalf("distance = %d, want 0", dist)
	}

	// Free inserts, unit deletes and renames.
	c2 := unitCosts()
	c2.InsDef = 0
	dist, _, _ = solve(t, parseTree("a"), parseTree("a(b(c,d),e)"), c2)
	if dist != 0 {
		t.Fatalf("distance = %d, want 0 (all inserts are free)", dist)
	}
}

// An inserted node must adopt the lifted children that belong under it; the
// script needs no move operation.
func TestInsertAdoptsLiftedChildren(t *testing.T) {
	c := unitCosts()
	c.RenDef = 10 // make renames unattractive so delete+insert wins
	src, tgt := parseTree("r(x(m),y)"), parseTree("r(z(m),y)")
	dist, ops, _ := solve(t, src, tgt, c)
	if dist != 2 {
		t.Fatalf("distance = %d, want 2 (delete x + insert z)", dist)
	}
	found := false
	for _, op := range ops {
		if op.Kind == "insert" && op.Label == "z" {
			found = true
			if op.Adopt != 1 {
				t.Fatalf("insert z adopts %d children, want 1 (m)", op.Adopt)
			}
		}
	}
	if !found {
		t.Fatal("script contains no insert of z")
	}
	res, _, total, err := applyScript(src, ops, c)
	if err != nil {
		t.Fatalf("applyScript: %v", err)
	}
	if total != 2 || !Equal(res, tgt) {
		t.Fatalf("applier: total=%d equal=%v", total, Equal(res, tgt))
	}
}

func TestIdenticalTrees(t *testing.T) {
	src := parseTree("a(b(c,d),e(f))")
	tgt := parseTree("a(b(c,d),e(f))")
	dist, ops, m := solve(t, src, tgt, unitCosts())
	if dist != 0 || len(ops) != 0 {
		t.Fatalf("dist=%d ops=%d, want 0/0", dist, len(ops))
	}
	if len(m.pairs) != 6 || len(m.del) != 0 || len(m.ins) != 0 {
		t.Fatalf("mapping = %+v", m)
	}
}

func TestParseCostsValidation(t *testing.T) {
	if _, err := parseCosts(costsJSON{Delete: map[string]int{"a": -1}}); err == nil {
		t.Fatal("negative delete cost accepted")
	}
	if _, err := parseCosts(costsJSON{Rename: map[string]int{"ab": 1}}); err == nil {
		t.Fatal("malformed rename key accepted")
	}
	ct, err := parseCosts(costsJSON{
		Insert: map[string]int{"*": 2, "a": 0},
		Rename: map[string]int{"a>b": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ct.InsCost("a") != 0 || ct.InsCost("zzz") != 2 || ct.RenCost("a", "b") != 3 ||
		ct.RenCost("b", "a") != 1 || ct.RenCost("q", "q") != 0 {
		t.Fatalf("bad cost table: %+v", ct)
	}
}

func randTree(r *rand.Rand, labels []string, maxNodes int) *Node {
	n := 1 + r.Intn(maxNodes)
	root := &Node{Label: labels[r.Intn(len(labels))]}
	all := []*Node{root}
	for i := 1; i < n; i++ {
		p := all[r.Intn(len(all))]
		nn := &Node{Label: labels[r.Intn(len(labels))]}
		pos := r.Intn(len(p.Children) + 1)
		p.Children = append(p.Children, nil)
		copy(p.Children[pos+1:], p.Children[pos:])
		p.Children[pos] = nn
		all = append(all, nn)
	}
	return root
}

func randCosts(r *rand.Rand, labels []string) *CostTable {
	c := &CostTable{Ins: map[string]int{}, Del: map[string]int{}, Ren: map[[2]string]int{}}
	rnd := func() int {
		if r.Intn(4) == 0 {
			return 0 // exercise zero-cost paths regularly
		}
		return 1 + r.Intn(3)
	}
	c.InsDef, c.DelDef, c.RenDef = rnd(), rnd(), rnd()
	for _, l := range labels {
		if r.Intn(2) == 0 {
			c.Ins[l] = rnd()
		}
		if r.Intn(2) == 0 {
			c.Del[l] = rnd()
		}
		for _, l2 := range labels {
			if l != l2 && r.Intn(3) == 0 {
				c.Ren[[2]string{l, l2}] = rnd()
			}
		}
	}
	return c
}

// Randomized cross-check: the dynamic program, the generated script checked
// by the independent applier, and exhaustive search must all agree.
func TestRandomizedCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	labels := []string{"a", "b", "c"}
	bruteChecked := 0
	for it := 0; it < 250; it++ {
		src := randTree(r, labels, 5)
		tgt := randTree(r, labels, 5)
		c := randCosts(r, labels)
		dist, ops, m, err := solveAll(src, tgt, c)
		if err != nil {
			t.Fatalf("it %d: solveAll: %v\nsrc=%s\ntgt=%s", it, err,
				SerializeForest([]*Node{src}), SerializeForest([]*Node{tgt}))
		}
		_ = m
		if err := verifyScript(src, tgt, ops, dist, c); err != nil {
			t.Fatalf("it %d: verifyScript: %v\nsrc=%s\ntgt=%s\nops=%v", it, err,
				SerializeForest([]*Node{src}), SerializeForest([]*Node{tgt}), ops)
		}
		if Size(src)+Size(tgt) <= 6 {
			bd, ok := bruteForceDistance(src, tgt, c, 200000)
			if ok {
				bruteChecked++
				if bd != dist {
					t.Fatalf("it %d: brute=%d dp=%d\nsrc=%s\ntgt=%s\ncosts=%+v", it, bd, dist,
						SerializeForest([]*Node{src}), SerializeForest([]*Node{tgt}), c)
				}
			}
		}
	}
	if bruteChecked < 50 {
		t.Fatalf("only %d cases brute-force checked; test too weak", bruteChecked)
	}
	fmt.Printf("brute-force confirmed %d random cases\n", bruteChecked)
}

// The mapping must respect the tree-edit mapping axioms.
func TestMappingAxioms(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	labels := []string{"a", "b"}
	for it := 0; it < 200; it++ {
		src := randTree(r, labels, 6)
		tgt := randTree(r, labels, 6)
		c := randCosts(r, labels)
		t1 := indexTree(src)
		t2 := indexTree(tgt)
		eng := newEngine(t1, t2, c)
		eng.distance()
		m := eng.backtrace()
		seen1, seen2 := map[int]bool{}, map[int]bool{}
		for _, p := range m.pairs {
			a, b := p[0], p[1]
			if seen1[a] || seen2[b] {
				t.Fatalf("it %d: pair %v not one-to-one", it, p)
			}
			seen1[a], seen2[b] = true, true
		}
		for _, p := range m.pairs {
			for _, q := range m.pairs {
				anc1 := isAncestor(t1, p[0], q[0])
				anc2 := isAncestor(t2, p[1], q[1])
				if anc1 != anc2 {
					t.Fatalf("it %d: ancestor order violated by %v and %v", it, p, q)
				}
				sib1 := t1.pre[p[0]] < t1.pre[q[0]]
				sib2 := t2.pre[p[1]] < t2.pre[q[1]]
				if sib1 != sib2 {
					t.Fatalf("it %d: sibling order violated by %v and %v", it, p, q)
				}
			}
		}
	}
}

func isAncestor(t *indexedTree, anc, x int) bool {
	return t.pre[anc] < t.pre[x] && anc > x
}
