package ast_test

import (
	"strings"
	"testing"

	"example.org/netsec/q0509/ast"
)

func TestStatementRendering(t *testing.T) {
	update := ast.Update(
		ast.Table("assets"),
		ast.Or(
			ast.Eq(ast.Col("id"), ast.Lit(int64(1))),
			ast.Eq(ast.Lit(int64(1)), ast.Lit(int64(1))),
		),
		ast.Set("status", ast.Lit("archived")),
	)

	got := update.SQL()
	want := `UPDATE "assets" SET "status" = 'archived' WHERE "id" = 1 OR 1 = 1`
	if got != want {
		t.Fatalf("update render:\n got: %s\nwant: %s", got, want)
	}

	insert := ast.Insert(
		ast.Table("assets"),
		[]string{"id", "owner"},
		[]ast.Expr{ast.Lit(int64(1)), ast.Lit("alice")},
		[]ast.Expr{ast.Lit(int64(2)), ast.Lit("bob")},
	)
	if !strings.Contains(insert.SQL(), `INSERT INTO "assets" ("id", "owner") VALUES (1, 'alice'), (2, 'bob')`) {
		t.Fatalf("unexpected insert render: %s", insert.SQL())
	}

	batch := ast.Batch(
		ast.Delete(ast.Table("assets"), ast.Eq(ast.Col("id"), ast.Lit(int64(1)))),
		update,
	)
	if !strings.Contains(batch.SQL(), "; UPDATE") {
		t.Fatalf("batch should join statements with ';': %s", batch.SQL())
	}
}

func TestInspectFindsSubqueries(t *testing.T) {
	stmt := ast.Delete(
		ast.Table("assets"),
		ast.Eq(
			ast.Col("id"),
			ast.ScalarSubquery(ast.Select(ast.Table("assets"), nil)),
		),
	)

	found := false
	ast.Inspect(stmt, func(node ast.Node) bool {
		if _, ok := node.(*ast.SubqueryExpr); ok {
			found = true
			return false
		}
		return true
	})
	if !found {
		t.Fatal("Inspect did not locate the nested subquery node")
	}
}

func TestLiteralRendering(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{nil, "NULL"},
		{"o'clock", "'o''clock'"},
		{true, "TRUE"},
		{int64(-7), "-7"},
		{[]byte{0xab}, "X'AB'"},
		{3.5, "3.5"},
	}
	for _, tc := range cases {
		if got := ast.Lit(tc.value).SQL(); got != tc.want {
			t.Fatalf("literal %v: got %s, want %s", tc.value, got, tc.want)
		}
	}
}
