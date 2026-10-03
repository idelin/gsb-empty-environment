package decor

import "testing"

func TestParseAcceptanceQuery(t *testing.T) {
	stmt, err := Parse("SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(stmt.From) != 1 || stmt.From[0].Name != "depts" || stmt.From[0].Alias != "d" {
		t.Fatalf("unexpected FROM: %+v", stmt.From)
	}
	ex, ok := stmt.Where.(*ExistsExpr)
	if !ok {
		t.Fatalf("WHERE is %T, want *ExistsExpr", stmt.Where)
	}
	sub := ex.Subquery
	if len(sub.From) != 1 || sub.From[0].Name != "emps" || sub.From[0].Alias != "e" {
		t.Fatalf("unexpected subquery FROM: %+v", sub.From)
	}
	be, ok := sub.Where.(*BinaryExpr)
	if !ok || be.Op != "=" {
		t.Fatalf("subquery WHERE is %T, want equality", sub.Where)
	}
	l, lok := be.Left.(*ColumnRef)
	r, rok := be.Right.(*ColumnRef)
	if !lok || !rok || l.Table != "e" || l.Name != "dept_id" || r.Table != "d" || r.Name != "id" {
		t.Fatalf("unexpected correlation predicate: %s", RenderExpr(be))
	}
}

func TestParseInSubquery(t *testing.T) {
	stmt, err := Parse("SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = 'EU')")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in, ok := stmt.Where.(*InExpr)
	if !ok || in.Subquery == nil {
		t.Fatalf("WHERE is %T, want *InExpr with subquery", stmt.Where)
	}
	if in.Negated {
		t.Fatal("IN wrongly marked negated")
	}
}

func TestParseNotInList(t *testing.T) {
	stmt, err := Parse("SELECT * FROM emps e WHERE e.id NOT IN (1, 2, 3)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in, ok := stmt.Where.(*InExpr)
	if !ok || !in.Negated || len(in.List) != 3 {
		t.Fatalf("unexpected NOT IN parse: %+v", stmt.Where)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	queries := []string{
		"SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)",
		"SELECT d.id, d.name FROM depts d WHERE d.budget > 100 AND d.region = 'EU'",
		"SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d)",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("parse %q: %v", q, err)
		}
		rendered := RenderSelect(stmt)
		if _, err := Parse(rendered); err != nil {
			t.Fatalf("re-parse of %q failed: %v", rendered, err)
		}
	}
}
