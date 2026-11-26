package joinenum

import (
	"fmt"
	"sort"
	"testing"
)

type bnode struct {
	op          *Operator
	left, right *bnode
	leaf        int
}

func genTrees(leaves []int, ops []*Operator) []*bnode {
	if len(leaves) == 1 {
		if len(ops) != 0 {
			return nil
		}
		return []*bnode{{leaf: leaves[0]}}
	}
	var out []*bnode
	n := len(leaves)
	for mask := 1; mask < (1<<n)-1; mask++ {
		var l, r []int
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				l = append(l, leaves[i])
			} else {
				r = append(r, leaves[i])
			}
		}
		if len(l)-1+len(r)-1+1 != len(ops) {
			continue
		}
		for oi, op := range ops {
			rest := make([]*Operator, 0, len(ops)-1)
			rest = append(rest, ops[:oi]...)
			rest = append(rest, ops[oi+1:]...)
			for om := 0; om < (1 << len(rest)); om++ {
				var lo, ro []*Operator
				for i, x := range rest {
					if om&(1<<i) != 0 {
						lo = append(lo, x)
					} else {
						ro = append(ro, x)
					}
				}
				if len(lo) != len(l)-1 || len(ro) != len(r)-1 {
					continue
				}
				for _, lt := range genTrees(l, lo) {
					for _, rt := range genTrees(r, ro) {
						out = append(out, &bnode{op: op, left: lt, right: rt})
					}
				}
			}
		}
	}
	return out
}

func (q *Query) planFromBNode(n *bnode, kindsAsInner bool) (*Plan, error) {
	if n.op == nil {
		return &Plan{
			Kind:        InnerJoin,
			Leaf:        n.leaf,
			Relations:   Of(n.leaf),
			Schema:      Of(n.leaf),
			Cardinality: q.relations[n.leaf].Cardinality,
			Encoding:    q.names[n.leaf],
		}, nil
	}
	l, err := q.planFromBNode(n.left, kindsAsInner)
	if err != nil {
		return nil, err
	}
	r, err := q.planFromBNode(n.right, kindsAsInner)
	if err != nil {
		return nil, err
	}
	kind := n.op.Kind
	if kindsAsInner {
		kind = InnerJoin
	}
	schema := l.Schema.Union(r.Schema)
	if !kindsAsInner && (kind == SemiJoin || kind == AntiJoin) {
		schema = l.Schema
	}
	out, local, err := q.model.Join(kind, l.Cardinality, r.Cardinality, n.op.Pred.Selectivity)
	if err != nil {
		return nil, err
	}
	cost, _ := checkedAdd(l.Cost, r.Cost)
	cost, _ = checkedAdd(cost, local)
	return &Plan{
		Kind:        kind,
		Leaf:        -1,
		Pred:        n.op.Pred,
		Left:        l,
		Right:       r,
		Relations:   l.Relations.Union(r.Relations),
		Schema:      schema,
		Cost:        cost,
		Cardinality: out,
		Encoding:    string(kind.Token()) + "(" + l.Encoding + "," + r.Encoding + ")",
	}, nil
}

func smallDBs(nrels int) []Database {
	opts := [][]Row{nil, rows(1), rows(2), rows(3), rows(1, 2), rows(1, 3), rows(2, 3)}
	var out []Database
	var rec func(int, [][]Row)
	rec = func(i int, cur [][]Row) {
		if i == nrels {
			out = append(out, NewDatabase(cur))
			return
		}
		for _, o := range opts {
			next := make([][]Row, len(cur)+1)
			copy(next, cur)
			next[i] = o
			rec(i+1, next)
		}
	}
	rec(0, nil)
	return out
}

func sameBag(a, b []Tuple) bool {
	x, y := CanonicalRows(a), CanonicalRows(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func bruteMinCost(t *testing.T, q *Query) (int64, string, int) {
	t.Helper()
	orig, err := q.OriginalPlan()
	if err != nil {
		t.Fatal(err)
	}
	leaves := q.allRels.Elements()
	ops := q.Operators()
	trees := genTrees(leaves, ops)
	dbs := smallDBs(len(q.relations))
	origRows := make([][]Tuple, len(dbs))
	for i, db := range dbs {
		origRows[i], err = q.Evaluate(orig, db)
		if err != nil {
			t.Fatal(err)
		}
	}
	var best int64 = 1<<62 - 1
	var bestEnc string
	count := 0
	for _, bt := range trees {
		p, err := q.planFromBNode(bt, false)
		if err != nil {
			t.Fatal(err)
		}
		equiv := true
		for di, db := range dbs {
			got, err := q.Evaluate(p, db)
			if err != nil {
				// Predicate references unavailable relations etc.
				equiv = false
				break
			}
			if !sameBag(origRows[di], got) {
				equiv = false
				break
			}
		}
		if !equiv {
			continue
		}
		count++
		if p.Cost < best || (p.Cost == best && p.Encoding < bestEnc) {
			best = p.Cost
			bestEnc = p.Encoding
		}
	}
	return best, bestEnc, count
}

func TestOptimalMatchesSemanticBruteForce(t *testing.T) {
	cases := []struct {
		name string
		root *Node
	}{
		{
			name: "l-asscom",
			root: Inner(LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1)), Rel(2), eqPred("q_AC", 1, 2, 0, 2)),
		},
		{
			name: "left-association",
			root: LeftOuter(LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1)), Rel(2), eqPred("q_BC", 1, 2, 1, 2)),
		},
		{
			name: "anti-preserved-side",
			root: Anti(LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1)), Rel(2), eqPred("q_AC", 1, 2, 0, 2)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := mustQuery(t, testRelations()[:3], tc.root)
			res := mustOptimize(t, q)
			best, bestEnc, count := bruteMinCost(t, q)
			if res.Cost != best {
				t.Fatalf("optimizer cost %d (%s), brute-force semantic min %d (%s), %d equivalent trees",
					res.Cost, res.Encoding, best, bestEnc, count)
			}
		})
	}
}

func TestUnrestrictedIsLowerBound(t *testing.T) {
	rels := testRelations()[:3]
	root := Anti(LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1)), Rel(2), eqPred("q_BC", 1, 2, 1, 2))
	q := mustQuery(t, rels, root)
	restricted := mustOptimize(t, q)
	free, err := q.EnumerateUnrestricted()
	if err != nil {
		t.Fatal(err)
	}
	if restricted.Cost < free.Cost {
		t.Fatalf("conflict-aware cost %d is below unrestricted lower bound %d", restricted.Cost, free.Cost)
	}
	if restricted.Cost == free.Cost {
		t.Fatalf("expected the outer/semi restrictions to lose to the unrestricted all-inner tree")
	}
}

func TestPureInnerMatchesUnrestrictedSearch(t *testing.T) {
	rels := testRelations()[:4]
	ab := Inner(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1))
	cd := Inner(Rel(2), Rel(3), eqPred("p_CD", 1, 2, 2, 3))
	root := Inner(ab, cd, eqPred("q_BC", 1, 2, 1, 2))
	q := mustQuery(t, rels, root)
	restricted := mustOptimize(t, q)
	free, err := q.EnumerateUnrestricted()
	if err != nil {
		t.Fatal(err)
	}
	if restricted.Cost != free.Cost {
		t.Fatalf("pure inner cost %d != unrestricted %d", restricted.Cost, free.Cost)
	}
	assertSameResult(t, q, restricted.Plan)
}

func TestTieBreakPrefersLexicographicPreorder(t *testing.T) {
	rels := []Relation{
		{Name: "A", Cardinality: 2},
		{Name: "B", Cardinality: 2},
		{Name: "C", Cardinality: 2},
	}
	// Two symmetric inner joins with identical costs.
	root := Inner(Inner(Rel(0), Rel(1), eqPred("p", 1, 1, 0, 1)), Rel(2), eqPred("q", 1, 1, 1, 2))
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	// The lexicographically smallest full encoding is I(A,I(B,C)) before
	// I(I(A,B),C) because '(' < 'A' at position 1.
	if res.Encoding != "I(A,I(B,C))" {
		t.Fatalf("tie-break encoding = %q, want I(A,I(B,C))", res.Encoding)
	}
}

func TestRulesAreDeterministic(t *testing.T) {
	rels := testRelations()[:3]
	root := Anti(LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1)), Rel(2), eqPred("q_BC", 1, 2, 1, 2))
	q := mustQuery(t, rels, root)
	a := fmt.Sprint(sortedRuleStrings(q))
	q2 := mustQuery(t, rels, root)
	b := fmt.Sprint(sortedRuleStrings(q2))
	if a != b {
		t.Fatalf("rules not deterministic:\n%s\n%s", a, b)
	}
	rules := q.ConflictRules()
	ids := make([]int, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	sort.Ints(ids)
	for i := range ids {
		if ids[i] != i {
			t.Fatalf("rule IDs are not dense/ordered: %v", ids)
		}
	}
}
