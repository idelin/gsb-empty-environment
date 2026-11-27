package outerjoin

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func testSchema() Schema {
	return mergeSchema(
		Relation("a", "id", "y").Schema(),
		Relation("b", "id", "aid", "x").Schema(),
		Relation("c", "bid", "z").Schema(),
	)
}

func aRow(id, y Value) Row {
	return NewRow().Set("a", "id", id).Set("a", "y", y)
}

func bRow(id, aid, x Value) Row {
	return NewRow().Set("b", "id", id).Set("b", "aid", aid).Set("b", "x", x)
}

func cRow(bid, z Value) Row {
	return NewRow().Set("c", "bid", bid).Set("c", "z", z)
}

func acceptanceDB() Database {
	return Database{
		"a": {
			aRow(1, 10),
			aRow(2, 20),
			aRow(2, 20),
			aRow(3, nil),
			aRow(nil, 40),
			aRow(5, 20),
		},
		"b": {
			bRow(101, 1, 1),
			bRow(102, 1, 1),
			bRow(103, 1, nil),
			bRow(104, 2, 2),
			bRow(105, 2, 2),
			bRow(106, 3, 3),
			bRow(107, nil, nil),
			bRow(108, 99, nil),
		},
		"c": {
			cRow(101, 1),
			cRow(101, 1),
			cRow(102, nil),
			cRow(104, 2),
			cRow(999, 2),
			cRow(nil, 3),
			cRow(105, nil),
		},
	}
}

func TestNullRejectingThreeValuedBoundaries(t *testing.T) {
	schema := testSchema()
	bx := Col("b", "x")
	ay := Col("a", "y")

	tests := []struct {
		name   string
		pred   Pred
		target string
		wantNR bool
	}{
		{"not equality on null target", Not(EqPred(bx, Lit(1))), "b", true},
		{"is null is a true rescue", IsNull(bx), "b", false},
		{"is not null rejects null target", IsNotNull(bx), "b", true},
		{"not is null becomes false on null target", Not(IsNull(bx)), "b", true},
		{"coalesce default equality is rescued", EqPred(Coalesce(bx, Lit(0)), Lit(0)), "b", false},
		{"or can be rescued by other relation", Or(EqPred(bx, Lit(1)), EqPred(ay, Lit(2))), "b", false},
		{"not-or remains unknown or false", Not(Or(EqPred(bx, Lit(1)), EqPred(ay, Lit(2)))), "b", true},
		{"and cannot become true with unknown target branch", And(EqPred(bx, Lit(1)), EqPred(ay, Lit(2))), "b", true},
		{"or of two target comparisons remains unknown", Or(EqPred(bx, Lit(1)), EqPred(bx, Lit(2))), "b", true},
		{"coalesce can be rescued by other relation", EqPred(Coalesce(bx, ay), Lit(1)), "b", false},
		{"coalesce rejects both source relations", EqPred(Coalesce(bx, ay), Lit(1)), "a", false},
		{"in rejects null target", In(bx, Lit(1), Lit(2)), "b", true},
		{"not in still unknown on null target", NotIn(bx, Lit(1)), "b", true},
		{
			"case else comparison rejects target",
			EqPred(Case(When(EqPred(bx, Lit(1)), Lit(1))).WithElse(Lit(0)), Lit(1)),
			"b", true,
		},
		{
			"case is-null branch rescues target",
			EqPred(Case(When(IsNull(bx), Lit(1))).WithElse(Lit(0)), Lit(1)),
			"b", false,
		},
		{
			"case true branch can rescue through other relation",
			EqPred(Case(When(EqPred(ay, Lit(20)), Lit(1))).WithElse(Lit(0)), Lit(1)),
			"b", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNR, witness, err := IsNullRejecting(tt.pred, schema, tt.target)
			if err != nil {
				t.Fatalf("IsNullRejecting returned error: %v", err)
			}
			if gotNR != tt.wantNR {
				t.Fatalf("IsNullRejecting=%v, want %v; witness=%v", gotNR, tt.wantNR, Row(witness))
			}
			if !tt.wantNR {
				if witness == nil {
					t.Fatalf("expected TRUE witness")
				}
				value, err := EvalPred(tt.pred, Row(witness))
				if err != nil {
					t.Fatalf("witness evaluation error: %v", err)
				}
				if value != True {
					t.Fatalf("witness evaluated to %s, want TRUE: %v", value, RowKey(Row(witness)))
				}
			}
		})
	}

	bothNR, _, err := IsNullRejecting(EqPred(Coalesce(bx, ay), Lit(1)), schema, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !bothNR {
		t.Fatalf("COALESCE(b.x,a.y)=1 must be null-rejecting on {a,b}")
	}
}

func TestWhereDegradationAndOnTaboo(t *testing.T) {
	a := Relation("a", "id", "y")
	b := Relation("b", "id", "aid", "x")
	pAB := EqPred(Col("a", "id"), Col("b", "aid"))

	t.Run("where not equality degrades left to inner", func(t *testing.T) {
		q := Query{
			From:  Join(Left, a, b, pAB),
			Where: Not(EqPred(Col("b", "x"), Lit(1))),
		}
		degraded, report, err := DegradeQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		if !report.Changed || len(report.Steps) != 1 {
			t.Fatalf("expected one degradation, got %+v", report)
		}
		if degraded.From.(*JoinNode).Type != Inner {
			t.Fatalf("expected INNER, got %s", degraded.From.(*JoinNode).Type)
		}
		assertQueriesBagEqual(t, q, degraded)
	})

	nonRejecting := map[string]Pred{
		"is null":     IsNull(Col("b", "x")),
		"coalesce":    EqPred(Coalesce(Col("b", "x"), Lit(0)), Lit(0)),
		"disjunction": Or(EqPred(Col("b", "x"), Lit(1)), EqPred(Col("a", "y"), Lit(2))),
	}
	for name, where := range nonRejecting {
		t.Run("where keeps left for "+name, func(t *testing.T) {
			q := Query{From: Join(Left, a, b, pAB), Where: where}
			degraded, report, err := DegradeQuery(q)
			if err != nil {
				t.Fatal(err)
			}
			if report.Changed {
				t.Fatalf("non-null-rejecting predicate degraded plan: %s", FormatDegradeReport(report))
			}
			if degraded.From.(*JoinNode).Type != Left {
				t.Fatalf("join type changed to %s", degraded.From.(*JoinNode).Type)
			}
		})
	}

	t.Run("on predicate never degrades its own outer join", func(t *testing.T) {
		on := And(
			EqPred(Col("a", "id"), Col("b", "aid")),
			Not(EqPred(Col("b", "x"), Lit(1))),
		)
		q := Query{From: Join(Left, a, b, on)}
		degraded, report, err := DegradeQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		if report.Changed {
			t.Fatalf("ON predicate caused degradation: %s", FormatDegradeReport(report))
		}
		if degraded.From.(*JoinNode).Type != Left {
			t.Fatalf("ON predicate changed join type to %s", degraded.From.(*JoinNode).Type)
		}
		rows, err := Evaluate(q, acceptanceDB())
		if err != nil {
			t.Fatal(err)
		}
		foundAOnly := false
		for _, row := range rows {
			if row[Ref{"a", "id"}] != nil && row[Ref{"b", "id"}] == nil {
				foundAOnly = true
			}
		}
		if !foundAOnly {
			t.Fatalf("expected preserved unmatched A row from LEFT JOIN")
		}
	})
}

func TestFullDegradationSidesAndCascade(t *testing.T) {
	a := Relation("a", "id", "y")
	b := Relation("b", "id", "aid", "x")
	c := Relation("c", "bid", "z")
	pAB := EqPred(Col("a", "id"), Col("b", "aid"))

	tests := []struct {
		name     string
		where    Pred
		wantType JoinType
		mapping  string
	}{
		{"left outer rows rejected by NR-on-right gives RIGHT", EqPred(Col("b", "x"), Lit(1)), Right, "FULL -> RIGHT"},
		{"right outer rows rejected by NR-on-left gives LEFT", EqPred(Col("a", "y"), Lit(1)), Left, "FULL -> LEFT"},
		{
			"both target sides give inner",
			And(EqPred(Col("a", "y"), Lit(1)), EqPred(Col("b", "x"), Lit(1))),
			Inner,
			"FULL -> INNER",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := Query{From: Join(Full, a, b, pAB), Where: tt.where}
			facts, err := AnalyzeTopNullSides(q)
			if err != nil {
				t.Fatal(err)
			}
			if got := facts.PromptMapping(); got != tt.mapping {
				t.Fatalf("mapping=%q want %q facts=%+v", got, tt.mapping, facts)
			}
			degraded, _, err := DegradeQuery(q)
			if err != nil {
				t.Fatal(err)
			}
			if got := degraded.From.(*JoinNode).Type; got != tt.wantType {
				t.Fatalf("FULL degraded to %s, want %s", got, tt.wantType)
			}
			assertQueriesBagEqual(t, q, degraded)
		})
	}

	t.Run("cascade through nested left joins", func(t *testing.T) {
		ab := Join(Left, a, b, pAB)
		tree := Join(Left, ab, c, EqPred(Col("b", "id"), Col("c", "bid")))
		q := Query{
			From:  tree,
			Where: And(EqPred(Col("b", "x"), Lit(1)), EqPred(Col("c", "z"), Lit(1))),
		}
		degraded, report, err := DegradeQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Steps) != 2 {
			t.Fatalf("expected two cascade steps, got %+v", report.Steps)
		}
		root := degraded.From.(*JoinNode)
		if root.Type != Inner || root.Left.(*JoinNode).Type != Inner {
			t.Fatalf("expected both joins INNER, got root=%s child=%s", root.Type, root.Left.(*JoinNode).Type)
		}
		assertQueriesBagEqual(t, q, degraded)
	})
}

func TestGLAssociativityAndBagEquivalence(t *testing.T) {
	a := Relation("a", "id", "y")
	b := Relation("b", "id", "aid", "x")
	c := Relation("c", "bid", "z")
	pAB := EqPred(Col("a", "id"), Col("b", "aid"))
	strictBC := EqPred(Col("b", "id"), Col("c", "bid"))
	weakBC := Or(EqPred(Col("b", "x"), Lit(1)), EqPred(Col("c", "z"), Lit(2)))
	db := acceptanceDB()

	t.Run("legal left-left identity is enumerated and bag equal", func(t *testing.T) {
		schema := testSchema()
		cert, err := CheckGLLeftAssociativity(strictBC, schema, "b")
		if err != nil || !cert.Legal {
			t.Fatalf("cert=%+v err=%v", cert, err)
		}

		q := Query{From: Join(Left, Join(Left, a, b, pAB), c, strictBC)}
		verified, err := VerifyLegalPlans(q, db)
		if err != nil {
			t.Fatal(err)
		}
		if len(verified) < 3 {
			t.Fatalf("expected original plus rewrites, got %d", len(verified))
		}
		for _, plan := range verified {
			if plan.EvalError != nil {
				t.Fatalf("evaluation error for %s: %v", plan.Key, plan.EvalError)
			}
			if !plan.BagEqual {
				t.Fatalf("formal rewrite not bag-equivalent\nplan=%s\ndiff=%s", plan.Key, plan.Diff.Sample)
			}
		}
		foundRightAssoc := false
		for _, plan := range verified {
			if containsRightDeepLeftLeft(plan.Key) {
				foundRightAssoc = true
			}
		}
		if !foundRightAssoc {
			t.Fatalf("did not enumerate right-associated GL form")
		}
	})

	t.Run("illegal weak left-left rearrangement has bag counterexample", func(t *testing.T) {
		cert, err := CheckGLLeftAssociativity(weakBC, testSchema(), "b")
		if err != nil {
			t.Fatal(err)
		}
		if cert.Legal {
			t.Fatalf("weak predicate incorrectly certified: %+v", cert)
		}

		original := Query{From: Join(Left, Join(Left, a, b, pAB), c, weakBC)}
		naive := Query{From: Join(Left, a, Join(Left, b, c, weakBC), pAB)}
		equal, diff, err := VerifyQueriesBagEqual(original, naive, db)
		if err != nil {
			t.Fatal(err)
		}
		if equal {
			t.Fatalf("test database failed to produce weak-predicate counterexample")
		}
		if diff.OriginalSize == diff.RewriteSize && len(diff.OriginalOnly)+len(diff.RewriteOnly) == 0 {
			t.Fatalf("expected nonempty bag diff")
		}

		plans, err := EnumerateStrictEquivalentPlans(original, db)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range plans {
			if containsRightDeepWeakLeft(plan.Key) {
				t.Fatalf("illegal right-associated weak form was accepted: %s", plan.Key)
			}
		}
	})

	t.Run("full-full strict identity and weak rejection", func(t *testing.T) {
		strictAB := EqPred(Col("a", "id"), Col("b", "aid"))
		cert, err := CheckGLFullAssociativity(strictAB, strictBC, testSchema(), "b")
		if err != nil || !cert.Legal {
			t.Fatalf("full cert=%+v err=%v", cert, err)
		}
		q := Query{From: Join(Full, Join(Full, a, b, strictAB), c, strictBC)}
		assertEveryFormalPlanBagEqual(t, q, db)

		weakAB := Or(EqPred(Col("a", "id"), Col("b", "aid")), EqPred(Col("a", "y"), Lit(20)))
		badCert, err := CheckGLFullAssociativity(weakAB, strictBC, testSchema(), "b")
		if err != nil {
			t.Fatal(err)
		}
		if badCert.Legal {
			t.Fatalf("weak FULL predicate incorrectly certified")
		}
		original := Query{From: Join(Full, Join(Full, a, b, weakAB), c, strictBC)}
		naive := Query{From: Join(Full, a, Join(Full, b, c, strictBC), weakAB)}
		equal, _, err := VerifyQueriesBagEqual(original, naive, db)
		if err != nil {
			t.Fatal(err)
		}
		if equal {
			t.Fatalf("expected FULL weak-AB associativity counterexample")
		}
	})

	t.Run("inner left lifting and left promotion", func(t *testing.T) {
		innerLeft := Query{From: Join(Left, Join(Inner, a, b, pAB), c, strictBC)}
		assertEveryFormalPlanBagEqual(t, innerLeft, db)

		promote := Query{From: Join(Inner, Join(Left, a, b, pAB), c, strictBC)}
		verified, err := VerifyLegalPlans(promote, db)
		if err != nil {
			t.Fatal(err)
		}
		foundPromoted := false
		for _, plan := range verified {
			if !plan.BagEqual {
				t.Fatalf("promotion rewrite mismatch: %s %s", plan.Key, plan.Diff.Sample)
			}
			for _, step := range plan.RuleChain {
				if step == "INNER upper ON null-rejects middle: promote LEFT to INNER and associate" {
					foundPromoted = true
				}
			}
		}
		if !foundPromoted {
			t.Fatalf("expected LEFT promotion rule in chain")
		}
	})
}

func TestRandomDuplicateAndNullBagEquivalence(t *testing.T) {
	a := Relation("a", "id", "y")
	b := Relation("b", "id", "aid", "x")
	c := Relation("c", "bid", "z")
	pAB := EqPred(Col("a", "id"), Col("b", "aid"))
	pBC := EqPred(Col("b", "id"), Col("c", "bid"))

	queries := map[string]Query{
		"left-gl":    {From: Join(Left, Join(Left, a, b, pAB), c, pBC)},
		"full-gl":    {From: Join(Full, Join(Full, a, b, pAB), c, pBC)},
		"inner-left": {From: Join(Left, Join(Inner, a, b, pAB), c, pBC)},
		"promotion":  {From: Join(Inner, Join(Left, a, b, pAB), c, pBC)},
	}

	for seed := int64(0); seed < 40; seed++ {
		db := randomNullHeavyDB(rand.New(rand.NewSource(seed*7919 + 20261127)))
		for name, q := range queries {
			t.Run(fmt.Sprintf("seed-%d/%s", seed, name), func(t *testing.T) {
				verified, err := VerifyLegalPlans(q, db)
				if err != nil {
					t.Fatal(err)
				}
				for _, plan := range verified {
					if plan.EvalError != nil {
						t.Fatalf("eval error: %v", plan.EvalError)
					}
					if !plan.BagEqual {
						t.Fatalf("bag mismatch\nplan=%s\ndiff=%s", plan.Key, plan.Diff.Sample)
					}
				}
			})
		}
	}
}

func randomNullHeavyDB(rng *rand.Rand) Database {
	pickID := func(max int64) Value {
		if rng.Float64() < 0.38 {
			return nil
		}
		return int64(rng.Intn(int(max + 1)))
	}
	aRows := make([]Row, 0, 14)
	for i := 0; i < 14; i++ {
		aRows = append(aRows, aRow(pickID(5), pickID(4)))
	}
	bRows := make([]Row, 0, 18)
	for i := 0; i < 18; i++ {
		bRows = append(bRows, bRow(int64(100+i%8), pickID(5), pickID(3)))
	}
	cRows := make([]Row, 0, 16)
	for i := 0; i < 16; i++ {
		var bid Value
		if rng.Float64() < 0.35 {
			bid = nil
		} else {
			bid = int64(100 + rng.Intn(10))
		}
		cRows = append(cRows, cRow(bid, pickID(3)))
	}
	return Database{"a": aRows, "b": bRows, "c": cRows}
}

func assertQueriesBagEqual(t *testing.T, left, right Query) {
	t.Helper()
	equal, diff, err := VerifyQueriesBagEqual(left, right, acceptanceDB())
	if err != nil {
		t.Fatalf("evaluation error: %v", err)
	}
	if !equal {
		t.Fatalf("expected bag equality, diff sample: %s", diff.Sample)
	}
}

func assertEveryFormalPlanBagEqual(t *testing.T, q Query, db Database) {
	t.Helper()
	verified, err := VerifyLegalPlans(q, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range verified {
		if plan.EvalError != nil {
			t.Fatalf("evaluation error for %s: %v", plan.Key, plan.EvalError)
		}
		if !plan.BagEqual {
			t.Fatalf("formal rewrite not bag-equivalent\nplan=%s\ndiff=%+v", plan.Key, plan.Diff)
		}
	}
}

func containsRightDeepLeftLeft(key string) bool {
	return strings.Contains(key, "a LEFT JOIN (b LEFT JOIN c")
}

func containsRightDeepWeakLeft(key string) bool {
	return strings.Contains(key, "a LEFT JOIN (b LEFT JOIN c")
}
