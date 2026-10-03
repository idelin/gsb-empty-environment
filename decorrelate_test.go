package decor

import (
	"strings"
	"testing"
)

func mustRewrite(t *testing.T, q string) *Rewrite {
	t.Helper()
	stmt, err := Parse(q)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rw, err := Decorrelate(stmt)
	if err != nil {
		t.Fatalf("decorrelate: %v", err)
	}
	return rw
}

func TestDecorrelateExistsAcceptance(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)")
	if !rw.Changed {
		t.Fatal("expected decorrelation to fire")
	}
	want := "SELECT d.* FROM depts d SEMI JOIN (SELECT DISTINCT e.dept_id FROM emps e) e ON d.id = e.dept_id"
	if rw.SQL != want {
		t.Fatalf("rewritten SQL:\n got: %s\nwant: %s", rw.SQL, want)
	}
	if strings.Contains(strings.ToUpper(rw.SQL), "EXISTS") {
		t.Fatalf("rewritten SQL still contains EXISTS: %s", rw.SQL)
	}
	hsj, ok := rw.Plan.(*HashSemiJoin)
	if !ok {
		t.Fatalf("plan root is %T, want *HashSemiJoin", rw.Plan)
	}
	if len(hsj.LeftKeys) != 1 || RenderExpr(hsj.LeftKeys[0]) != "d.id" {
		t.Fatalf("unexpected left keys: %v", hsj.LeftKeys)
	}
	if len(hsj.RightKeys) != 1 || RenderExpr(hsj.RightKeys[0]) != "e.dept_id" {
		t.Fatalf("unexpected right keys: %v", hsj.RightKeys)
	}
	if _, ok := hsj.Right.(*Distinct); !ok {
		t.Fatalf("semi-join right side is %T, want *Distinct", hsj.Right)
	}
}

func TestDecorrelateIn(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = 'EU')")
	want := "SELECT e.* FROM emps e SEMI JOIN (SELECT DISTINCT d.id FROM depts d WHERE d.region = 'EU') d ON e.dept_id = d.id"
	if rw.SQL != want {
		t.Fatalf("rewritten SQL:\n got: %s\nwant: %s", rw.SQL, want)
	}
	if _, ok := rw.Plan.(*HashSemiJoin); !ok {
		t.Fatalf("plan root is %T, want *HashSemiJoin", rw.Plan)
	}
}

func TestDecorrelateUncorrelatedExists(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.vip = 1)")
	want := "SELECT d.* FROM depts d CROSS JOIN (SELECT 1 FROM emps e WHERE e.vip = 1 LIMIT 1) e"
	if rw.SQL != want {
		t.Fatalf("rewritten SQL:\n got: %s\nwant: %s", rw.SQL, want)
	}
	if _, ok := rw.Plan.(*CrossJoin); !ok {
		t.Fatalf("plan root is %T, want *CrossJoin", rw.Plan)
	}
}

func TestDecorrelateNonEqualityFails(t *testing.T) {
	stmt, err := Parse("SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.salary > d.min_salary)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Decorrelate(stmt); err == nil {
		t.Fatal("expected error for non-equality correlated predicate")
	} else if !strings.Contains(err.Error(), "non-equality") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecorrelateMultipleKeysAndLocalPred(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND e.region = d.region AND e.active = 1)")
	want := "SELECT d.* FROM depts d SEMI JOIN (SELECT DISTINCT e.dept_id, e.region FROM emps e WHERE e.active = 1) e ON d.id = e.dept_id AND d.region = e.region"
	if rw.SQL != want {
		t.Fatalf("rewritten SQL:\n got: %s\nwant: %s", rw.SQL, want)
	}
	hsj := rw.Plan.(*HashSemiJoin)
	if len(hsj.LeftKeys) != 2 || len(hsj.RightKeys) != 2 {
		t.Fatalf("want 2 join keys, got %d/%d", len(hsj.LeftKeys), len(hsj.RightKeys))
	}
}

func TestResidualOuterPredicateKept(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM depts d WHERE d.budget > 100 AND EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)")
	want := "SELECT d.* FROM depts d SEMI JOIN (SELECT DISTINCT e.dept_id FROM emps e) e ON d.id = e.dept_id WHERE d.budget > 100"
	if rw.SQL != want {
		t.Fatalf("rewritten SQL:\n got: %s\nwant: %s", rw.SQL, want)
	}
	if _, ok := rw.Plan.(*Filter); !ok {
		t.Fatalf("plan root is %T, want *Filter above the semi join", rw.Plan)
	}
}

func TestNoSubqueryUnchanged(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM depts d WHERE d.budget > 100")
	if rw.Changed {
		t.Fatal("no subquery present, nothing should change")
	}
	if rw.SQL != rw.Original {
		t.Fatalf("SQL changed: %s -> %s", rw.Original, rw.SQL)
	}
}

func TestNotInLeftAlone(t *testing.T) {
	rw := mustRewrite(t, "SELECT * FROM emps e WHERE e.dept_id NOT IN (SELECT d.id FROM depts d)")
	if rw.Changed {
		t.Fatal("NOT IN must not be rewritten to a semi join")
	}
}
