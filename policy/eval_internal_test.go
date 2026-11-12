package policy

import (
	"errors"
	"testing"

	"example.org/netsec/q0509/ast"
)

func TestEvaluatorThreeValuedLogic(t *testing.T) {
	ev := newEvaluator(Context{Caller: "alice", BlockNum: 7}, []string{"a", "b"})
	row := Row{"a": nil, "b": int64(2)}

	cases := []struct {
		name string
		expr ast.Expr
		want any
	}{
		{"NULL = 1", ast.Eq(ast.Col("a"), ast.Lit(int64(1))), nil},
		{"NULL IS NULL", ast.Null(ast.Col("a")), true},
		{"b IS NOT NULL", ast.NotNull(ast.Col("b")), true},
		{"NULL OR true", ast.Or(ast.Col("a"), ast.Lit(true)), true},
		{"NULL AND true", ast.And(ast.Col("a"), ast.Lit(true)), nil},
		{"NULL AND false", ast.And(ast.Null(ast.Col("a")), ast.Lit(false)), false},
		{"NOT NULL", ast.Not(ast.Col("a")), nil},
		{"caller", ast.Eq(ast.Caller(), ast.Lit("alice")), true},
		{"block_num", ast.Eq(ast.Block(), ast.Lit(int64(7))), true},
		{"IN hit", ast.In(ast.Col("b"), ast.Lit(int64(1)), ast.Lit(int64(2))), true},
		{"IN miss", ast.In(ast.Col("b"), ast.Lit(int64(9))), false},
		{"NOT IN hit", ast.NotIn(ast.Col("b"), ast.Lit(int64(2))), false},
		{"IN NULL member", ast.In(ast.Col("b"), ast.Lit(nil), ast.Lit(int64(9))), nil},
		{"coalesce", ast.Func("COALESCE", ast.Col("a"), ast.Lit("x")), "x"},
		{"lower", ast.Func("LOWER", ast.Lit("ABC")), "abc"},
		{"length", ast.Func("LENGTH", ast.Lit("four")), int64(4)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ev.eval(tc.expr, row)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if !valuesEqual(got, tc.want) {
				t.Fatalf("got %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

func TestEvaluatorArithmeticAndComparison(t *testing.T) {
	ev := newEvaluator(Context{}, []string{"n", "s"})
	row := Row{"n": int64(10), "s": "abc"}

	arith := []struct {
		expr ast.Expr
		want any
	}{
		{ast.Bin("+", ast.Col("n"), ast.Lit(int64(5))), int64(15)},
		{ast.Bin("/", ast.Col("n"), ast.Lit(int64(4))), int64(2)},
		{ast.Bin("%", ast.Col("n"), ast.Lit(int64(3))), int64(1)},
		{ast.Neg(ast.Col("n")), int64(-10)},
		{ast.Bin(">", ast.Col("n"), ast.Lit(int64(9))), true},
		{ast.Bin("<=", ast.Lit("a"), ast.Col("s")), true},
		{ast.Neq(ast.Col("s"), ast.Lit("abc")), false},
	}
	for _, tc := range arith {
		got, err := ev.eval(tc.expr, row)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr.SQL(), err)
		}
		if !valuesEqual(got, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.expr.SQL(), got, tc.want)
		}
	}

	if _, err := ev.eval(ast.Bin("=", ast.Col("n"), ast.Col("s")), row); !errors.Is(err, ErrBadStatement) {
		t.Fatalf("type-mismatched compare: expected ErrBadStatement, got %v", err)
	}
	if _, err := ev.eval(ast.Bin("/", ast.Col("n"), ast.Lit(int64(0))), row); !errors.Is(err, ErrBadStatement) {
		t.Fatalf("divide by zero: expected ErrBadStatement, got %v", err)
	}
	if _, err := ev.eval(ast.Func("nope"), row); !errors.Is(err, ErrBadStatement) {
		t.Fatalf("unknown function: expected ErrBadStatement, got %v", err)
	}
	if _, err := ev.eval(ast.Col("missing"), row); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("unknown column: expected ErrUnknownColumn, got %v", err)
	}
}

func TestMemoryTxnRollback(t *testing.T) {
	catalog := NewMemoryCatalog()
	catalog.CreateTable("t", []string{"id"})
	catalog.Seed("t", Row{"id": int64(1)})

	txn, err := catalog.Begin("t")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := txn.Insert(Row{"id": int64(2)}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := txn.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if rows := catalog.Rows("t"); len(rows) != 1 || rows[0]["id"] != int64(1) {
		t.Fatalf("rollback did not restore table: %v", rows)
	}

	// A fresh transaction must be available after rollback (lock released).
	txn2, err := catalog.Begin("t")
	if err != nil {
		t.Fatalf("Begin after rollback: %v", err)
	}
	if err := txn2.Delete(0); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := txn2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if rows := catalog.Rows("t"); len(rows) != 0 {
		t.Fatalf("commit did not publish deletion: %v", rows)
	}
}

func TestMergeWhereCases(t *testing.T) {
	user := ast.Eq(ast.Col("a"), ast.Lit(int64(1)))
	pred := ast.Eq(ast.Col("b"), ast.Caller())

	if MergeWhere(nil, nil) != nil {
		t.Fatal("both nil should merge to nil")
	}
	if got := MergeWhere(nil, pred); got != pred {
		t.Fatalf("nil user predicate should return policy predicate as-is, got %T", got)
	}
	if _, ok := MergeWhere(user, nil).(*ast.ParenExpr); !ok {
		t.Fatal("user-only merge must still parenthesise the user predicate")
	}
	both, ok := MergeWhere(user, pred).(*ast.BinaryExpr)
	if !ok || both.Operator != "AND" {
		t.Fatalf("full merge must be an AND binary node, got %T", MergeWhere(user, pred))
	}
	if _, ok := both.Left.(*ast.ParenExpr); !ok {
		t.Fatal("left side of merged AND must parenthesise the user predicate")
	}
}

func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a == b
}
