package outerjoin

import (
	"strings"
	"testing"
)

func TestRightJoinDegradationAndCommutativity(t *testing.T) {
	a := Relation("a", "id", "y")
	b := Relation("b", "id", "aid", "x")
	pAB := EqPred(Col("a", "id"), Col("b", "aid"))
	db := acceptanceDB()

	q := Query{From: Join(Right, a, b, pAB), Where: EqPred(Col("a", "y"), Lit(10))}
	degraded, report, err := DegradeQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Steps) != 1 || degraded.From.(*JoinNode).Type != Inner {
		t.Fatalf("RIGHT with left-null-rejecting WHERE should become INNER: %+v", report)
	}
	assertQueriesBagEqual(t, q, degraded)

	onTaboo := Query{From: Join(Right, a, b, EqPred(Col("a", "y"), Lit(10)))}
	_, report, err = DegradeQuery(onTaboo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed {
		t.Fatalf("RIGHT own ON predicate must not degrade join: %+v", report)
	}

	left := Query{From: Join(Left, a, b, pAB)}
	verified, err := VerifyLegalPlans(left, db)
	if err != nil {
		t.Fatal(err)
	}
	foundRightCommute := false
	for _, plan := range verified {
		if !plan.BagEqual {
			t.Fatalf("commuted plan differs: %s %s", plan.Key, plan.Diff.Sample)
		}
		if strings.Contains(plan.Key, "b RIGHT JOIN a") {
			foundRightCommute = true
		}
	}
	if !foundRightCommute {
		t.Fatalf("expected LEFT/RIGHT commutative form")
	}
}

func TestNullRejectionHandlesCorrelatedResidualPredicates(t *testing.T) {
	schema := testSchema()
	bx := Col("b", "x")
	ay := Col("a", "y")

	p := Or(
		EqPred(bx, Lit(1)),
		And(EqPred(ay, Lit(1)), Not(EqPred(ay, Lit(1)))),
	)
	nr, witness, err := IsNullRejecting(p, schema, "b")
	if err != nil {
		t.Fatal(err)
	}
	if !nr {
		t.Fatalf("correlated contradiction cannot rescue B; witness=%v", Row(witness))
	}

	relations, err := NullRejectingRelations(Not(EqPred(bx, Lit(1))), schema)
	if err != nil {
		t.Fatal(err)
	}
	foundB := false
	for _, item := range relations {
		if item.Relation == "b" {
			foundB = true
			if !item.NullRejecting {
				t.Fatalf("expected b to be null rejecting: %+v", item)
			}
		}
	}
	if !foundB {
		t.Fatalf("missing b in %+v", relations)
	}

	stringSchema := Schema{
		Ref{Relation: "b", Attr: "name"}: TypeString,
		Ref{Relation: "a", Attr: "y"}:    TypeInt,
	}
	name := Col("b", "name")
	nr, witness, err = IsNullRejecting(Not(EqPred(name, Lit("x"))), stringSchema, "b")
	if err != nil || !nr {
		t.Fatalf("string NOT equality nr=%v err=%v witness=%v", nr, err, Row(witness))
	}
	nr, witness, err = IsNullRejecting(
		Or(EqPred(name, Lit("x")), EqPred(ay, Lit(1))),
		stringSchema,
		"b",
	)
	if err != nil || nr {
		t.Fatalf("string OR rescue should not be NR: nr=%v err=%v witness=%v", nr, err, Row(witness))
	}

	boolSchema := Schema{
		Ref{Relation: "b", Attr: "active"}: TypeBool,
		Ref{Relation: "a", Attr: "y"}:      TypeInt,
	}
	nr, _, err = IsNullRejecting(BoolColumn("b", "active"), boolSchema, "b")
	if err != nil || !nr {
		t.Fatalf("NULL boolean column should be null rejecting: nr=%v err=%v", nr, err)
	}
	nr, witness, err = IsNullRejecting(
		Or(BoolColumn("b", "active"), EqPred(ay, Lit(1))),
		boolSchema,
		"b",
	)
	if err != nil || nr {
		t.Fatalf("boolean OR rescue should not be NR: nr=%v err=%v witness=%v", nr, err, Row(witness))
	}
}
