package joinenum

import (
	"sort"
	"testing"
)

func testRelations() []Relation {
	return []Relation{
		{Name: "A", Cardinality: 5, Attributes: []string{"k"}},
		{Name: "B", Cardinality: 4, Attributes: []string{"k"}},
		{Name: "C", Cardinality: 3, Attributes: []string{"k"}},
		{Name: "D", Cardinality: 2, Attributes: []string{"k"}},
	}
}

func rows(vals ...int64) []Row {
	out := make([]Row, len(vals))
	for i, v := range vals {
		out[i] = Row{"k": IntValue(v)}
	}
	return out
}

func testDB() Database {
	return NewDatabase([][]Row{
		rows(1, 2, 3, 4, 5),
		rows(1, 2, 3, 4),
		rows(1, 2, 3),
		rows(1, 2),
	})
}

func eqPred(name string, num, den int64, r1, r2 int) *Predicate {
	return Eq(name, Sel(num, den), Column{Rel: r1, Attr: "k"}, Column{Rel: r2, Attr: "k"})
}

func nullAcceptingEq(name string, num, den int64, r1, r2 int) *Predicate {
	left := Column{Rel: r1, Attr: "k"}
	right := Column{Rel: r2, Attr: "k"}
	return MatchPredicate(name, Sel(num, den), []int{r1, r2}, true, func(t Tuple) bool {
		a, ok := t[left]
		if !ok || a.Null {
			return true
		}
		b, ok := t[right]
		if !ok || b.Null {
			return true
		}
		return valueEqual(a, b)
	})
}

func mustQuery(t *testing.T, rels []Relation, root *Node) *Query {
	t.Helper()
	q, err := NewQuery(rels, root)
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	return q
}

func mustOptimize(t *testing.T, q *Query) *Result {
	t.Helper()
	res, err := q.Optimize()
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	return res
}

func assertSameResult(t *testing.T, q *Query, p *Plan) {
	t.Helper()
	db := testDB()
	db.Tables = db.Tables[:len(q.relations)]
	want, err := q.EvaluateOriginal(db)
	if err != nil {
		t.Fatalf("EvaluateOriginal: %v", err)
	}
	got, err := q.Evaluate(p, db)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	w := CanonicalRows(want)
	g := CanonicalRows(got)
	if len(w) != len(g) {
		t.Fatalf("result size %d != %d\n got=%v\nwant=%v", len(g), len(w), g, w)
	}
	for i := range w {
		if w[i] != g[i] {
			t.Fatalf("result differs at %d\n got=%v\nwant=%v", i, g[i], w[i])
		}
	}
}

func legalEncodings(t *testing.T, q *Query) []string {
	t.Helper()
	plans, err := q.enumerate(false)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	out := make([]string, 0, len(plans))
	for _, p := range plans {
		out = append(out, p.Encoding)
	}
	sort.Strings(out)
	return out
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// L-ASSCOM: an inner join may be pulled into the preserved (left) input of a
// left outer join.
func TestLAsscomWithLeftOuter(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := eqPred("q_AC", 1, 2, 0, 2)
	root := Inner(LeftOuter(Rel(0), Rel(1), p), Rel(2), qq)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)

	if got, want := res.Encoding, "L(I(A,C),B)"; got != want {
		t.Fatalf("optimal encoding = %q, want %q (cost %d)", got, want, res.Cost)
	}
	orig, err := q.OriginalPlan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost >= orig.Cost {
		t.Fatalf("reassociated cost %d did not improve original cost %d", res.Cost, orig.Cost)
	}
	encs := legalEncodings(t, q)
	if !containsString(encs, "L(I(A,C),B)") {
		t.Fatalf("L-ASSCOM plan missing from legal plans: %v", encs)
	}
	assertSameResult(t, q, res.Plan)
}

// The mirror orientation: an inner join above a left outer join may be pulled
// into the left-outer's preserved side.
func TestMirrorLAsscomWithLeftOuter(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_BC", 1, 2, 1, 2)
	qq := eqPred("q_AB", 1, 2, 0, 1)
	root := Inner(Rel(0), LeftOuter(Rel(1), Rel(2), p), qq)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	if res.Encoding != "L(I(A,B),C)" {
		t.Fatalf("optimal encoding = %q, want L(I(A,B),C)", res.Encoding)
	}
	assertSameResult(t, q, res.Plan)
}

// An anti join must not move into the null-supplying side of a left outer
// join.  This query has no legal reassociation other than the original tree.
func TestAntiCannotCrossLeftOuterNullSide(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := eqPred("q_BC", 1, 2, 1, 2)
	root := Anti(LeftOuter(Rel(0), Rel(1), p), Rel(2), qq)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	orig, _ := q.OriginalPlan()
	if res.Cost != orig.Cost {
		t.Fatalf("cost = %d, want original cost %d", res.Cost, orig.Cost)
	}
	if res.Encoding != "A(L(A,B),C)" {
		t.Fatalf("optimal encoding = %q, want original A(L(A,B),C)", res.Encoding)
	}
	encs := legalEncodings(t, q)
	if containsString(encs, "L(A,A(B,C))") {
		t.Fatalf("illegal null-side crossing was accepted: %v", encs)
	}
	found := false
	ops := q.Operators()
	for _, r := range q.ConflictRules() {
		if r.Kind == ForbidRight && r.Descendant == 0 && r.Ancestor == 1 {
			found = true
			if !r.T1.Equal(ops[0].Rels) || !r.T2.Equal(ops[1].Rels) {
				t.Fatalf("bad conflict rule payload: %v", r)
			}
		}
	}
	if !found {
		t.Fatalf("missing anti/left-outer conflict rule: %v", q.ConflictRules())
	}
	assertSameResult(t, q, res.Plan)
}

// The anti join may move into the preserved side of the left outer join.
func TestAntiMayMoveToPreservedSide(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := eqPred("q_AC", 1, 2, 0, 2)
	root := Anti(LeftOuter(Rel(0), Rel(1), p), Rel(2), qq)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	if res.Encoding != "L(A(A,C),B)" {
		t.Fatalf("optimal encoding = %q, want L(A(A,C),B)", res.Encoding)
	}
	assertSameResult(t, q, res.Plan)
}

// Two left outer joins may be associated when the inner predicate is
// null-rejecting with respect to the null-supplied side.
func TestLeftOuterAssociationNullRejecting(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := eqPred("q_BC", 1, 2, 1, 2)
	root := LeftOuter(LeftOuter(Rel(0), Rel(1), p), Rel(2), qq)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	if res.Encoding != "L(A,L(B,C))" {
		t.Fatalf("optimal encoding = %q, want L(A,L(B,C))", res.Encoding)
	}
	assertSameResult(t, q, res.Plan)
}

func TestLeftOuterAssociationMirror(t *testing.T) {
	rels := []Relation{
		{Name: "A", Cardinality: 2, Attributes: []string{"k"}},
		{Name: "B", Cardinality: 2, Attributes: []string{"k"}},
		{Name: "C", Cardinality: 10, Attributes: []string{"k"}},
	}
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := eqPred("q_BC", 1, 2, 1, 2)
	// Right-deep A LEFT (B LEFT C).  The mirror L-ASSCOM rule must still
	// admit the left-deep (A LEFT B) LEFT C tree.
	root := LeftOuter(Rel(0), LeftOuter(Rel(1), Rel(2), qq), p)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	assertSameResult(t, q, res.Plan)
	encs := legalEncodings(t, q)
	if !containsString(encs, "L(L(A,B),C)") {
		t.Fatalf("mirror association missing from legal plans: %v", encs)
	}
}

// With a null-preserving predicate the same association is not equivalent and
// must be rejected by the R-ASSCOM conflict rule.
func TestLeftOuterAssociationRequiresNullRejection(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := nullAcceptingEq("q_BC_null", 1, 2, 1, 2)
	root := LeftOuter(LeftOuter(Rel(0), Rel(1), p), Rel(2), qq)
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	if res.Encoding != "L(L(A,B),C)" {
		t.Fatalf("optimal encoding = %q, want original L(L(A,B),C)", res.Encoding)
	}
	encs := legalEncodings(t, q)
	if containsString(encs, "L(A,L(B,C))") {
		t.Fatalf("illegal association with null-preserving predicate accepted: %v", encs)
	}
	assertSameResult(t, q, res.Plan)
}

// A non-commutative chain keeps exactly the original order when no
// reassociation is covered by a legal rule.
func TestNonCommutativeChain(t *testing.T) {
	rels := testRelations()[:3]
	p := eqPred("p_AB", 1, 2, 0, 1)
	qq := nullAcceptingEq("q_BC_null", 1, 2, 1, 2)
	root := LeftOuter(LeftOuter(Rel(0), Rel(1), p), Rel(2), qq)
	q := mustQuery(t, rels, root)
	encs := legalEncodings(t, q)
	if len(encs) != 1 || encs[0] != "L(L(A,B),C)" {
		t.Fatalf("legal plans = %v, want only L(L(A,B),C)", encs)
	}
}

func TestTESMinimality(t *testing.T) {
	rels := testRelations()[:3]
	inner := Inner(Rel(1), Rel(2), eqPred("r_BC", 1, 1, 1, 2))
	root := LeftOuter(Rel(0), inner, eqPred("p_AB", 1, 1, 0, 1))
	q := mustQuery(t, rels, root)
	ops := q.Operators()
	var leftID, innerID int
	for _, op := range ops {
		switch op.Kind {
		case LeftOuterJoin:
			leftID = op.ID
		case InnerJoin:
			innerID = op.ID
		}
	}
	if got, want := q.TES(leftID), Of(0, 1); !got.Equal(want) {
		t.Fatalf("LEFT TES = %v, want %v", got, want)
	}
	if got, want := q.TES(innerID), Of(1, 2); !got.Equal(want) {
		t.Fatalf("INNER TES = %v, want %v", got, want)
	}

	semi := Semi(Rel(1), Rel(2), eqPred("s_BC", 1, 1, 1, 2))
	root2 := LeftOuter(Rel(0), semi, eqPred("p2_AB", 1, 1, 0, 1))
	q2 := mustQuery(t, rels, root2)
	for _, op := range q2.Operators() {
		if op.Kind == LeftOuterJoin {
			// The predicate only observes A and B; unreferenced right-input
			// relations such as C may be supplied by an ancestor.
			if got, want := q2.TES(op.ID), Of(0, 1); !got.Equal(want) {
				t.Fatalf("LEFT-over-SEMI TES = %v, want %v", got, want)
			}
		}
	}
}

func TestLeftOuterIsNotCommutative(t *testing.T) {
	rels := testRelations()[:2]
	root := LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1))
	q := mustQuery(t, rels, root)
	res := mustOptimize(t, q)
	if res.Encoding != "L(A,B)" {
		t.Fatalf("encoding = %q, want L(A,B)", res.Encoding)
	}
	if containsString(legalEncodings(t, q), "L(B,A)") {
		t.Fatalf("left outer join was illegally commuted")
	}
}
