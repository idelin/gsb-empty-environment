package policy_test

import (
	"errors"
	"strings"
	"testing"

	"example.org/netsec/q0509/ast"
	"example.org/netsec/q0509/policy"
)

const targetTable = "assets"

type fixture struct {
	catalog *policy.MemoryCatalog
	enf     *policy.Enforcer
	ctx     policy.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	catalog := policy.NewMemoryCatalog()
	catalog.CreateTable(targetTable, []string{"id", "owner", "name", "status"})
	catalog.Seed(targetTable,
		policy.Row{"id": int64(1), "owner": "alice", "name": "server-a", "status": "active"},
		policy.Row{"id": int64(2), "owner": "bob", "name": "server-b", "status": "active"},
		policy.Row{"id": int64(3), "owner": "carol", "name": "server-c", "status": "archived"},
	)

	p := policy.Policy{
		Table:            targetTable,
		UpdatableColumns: []string{"status", "name"},
		// Rows are only writable when their owner is the transaction caller.
		Where: ast.Eq(ast.Col("owner"), ast.Caller()),
		// Every new/updated row must stay owned by the caller.
		WithCheck: ast.Eq(ast.Col("owner"), ast.Caller()),
		MaxRows:   2,
	}

	ctx := policy.Context{Caller: "alice", BlockNum: 42}
	enf, err := policy.NewEnforcer(p, catalog)
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	return &fixture{catalog: catalog, enf: enf, ctx: ctx}
}

func (f *fixture) owners() []string {
	rows := f.catalog.Rows(targetTable)
	owners := make([]string, 0, len(rows))
	for _, r := range rows {
		owners = append(owners, r["owner"].(string))
	}
	return owners
}

// TestOrInjectionDelete is the headline attack: the attacker appends
// "OR 1=1" to a DELETE predicate hoping to wipe every row. The policy
// predicate is AND-merged outside the parenthesised user predicate, so only
// the caller's own row may be removed.
func TestOrInjectionDelete(t *testing.T) {
	f := newFixture(t)

	injection := ast.Or(
		ast.Eq(ast.Col("name"), ast.Lit("server-a")),
		ast.Eq(ast.Lit(int64(1)), ast.Lit(int64(1))),
	)
	stmt := ast.Delete(ast.Table(targetTable), injection)

	count, err := f.enf.Enforce(f.ctx, stmt)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 deleted row, got %d", count)
	}

	got := f.owners()
	want := []string{"bob", "carol"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("remaining owners = %v, want %v", got, want)
	}
}

// TestOrInjectionUpdate proves the same merge protects UPDATE.
func TestOrInjectionUpdate(t *testing.T) {
	f := newFixture(t)

	injection := ast.Or(
		ast.Eq(ast.Col("id"), ast.Lit(int64(1))),
		ast.Eq(ast.Lit(int64(1)), ast.Lit(int64(1))),
	)
	stmt := ast.Update(
		ast.Table(targetTable),
		injection,
		ast.Set("status", ast.Lit("pwned")),
	)

	count, err := f.enf.Enforce(f.ctx, stmt)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 updated row, got %d", count)
	}
	for _, row := range f.catalog.Rows(targetTable) {
		if row["owner"] != "alice" && row["status"] == "pwned" {
			t.Fatalf("attacker modified foreign row: %v", row)
		}
	}
}

// TestForbiddenSetColumn rejects an UPDATE that assigns a column outside the
// updatable-column whitelist before any row is touched.
func TestForbiddenSetColumn(t *testing.T) {
	f := newFixture(t)

	stmt := ast.Update(
		ast.Table(targetTable),
		ast.Eq(ast.Col("id"), ast.Lit(int64(1))),
		ast.Set("id", ast.Lit(int64(999))),
	)

	count, err := f.enf.Enforce(f.ctx, stmt)
	if !errors.Is(err, policy.ErrForbiddenColumn) {
		t.Fatalf("expected ErrForbiddenColumn, got count=%d err=%v", count, err)
	}
	if count != 0 {
		t.Fatalf("rejected statement reported %d affected rows", count)
	}

	row := f.catalog.Rows(targetTable)[0]
	if row["id"] != int64(1) {
		t.Fatalf("store changed despite rejection: %v", row)
	}

	// A second transaction must start, proving the failed one released locks.
	ok := ast.Update(ast.Table(targetTable),
		ast.Eq(ast.Col("id"), ast.Lit(int64(1))),
		ast.Set("status", ast.Lit("idle")))
	if _, err := f.enf.Enforce(f.ctx, ok); err != nil {
		t.Fatalf("follow-up statement after rejection failed: %v", err)
	}
}

// TestWithCheckInsert rolls back the entire multi-row INSERT when one row
// violates the WITH CHECK predicate.
func TestWithCheckInsert(t *testing.T) {
	f := newFixture(t)

	goodRow := []ast.Expr{ast.Lit(int64(10)), ast.Lit("alice"), ast.Lit("new-a"), ast.Lit("active")}
	badRow := []ast.Expr{ast.Lit(int64(12)), ast.Lit("mallory"), ast.Lit("new-c"), ast.Lit("active")}

	stmt := ast.Insert(
		ast.Table(targetTable),
		[]string{"id", "owner", "name", "status"},
		goodRow, badRow,
	)

	count, err := f.enf.Enforce(f.ctx, stmt)
	if !errors.Is(err, policy.ErrCheckViolation) {
		t.Fatalf("expected ErrCheckViolation, got count=%d err=%v", count, err)
	}
	if len(f.catalog.Rows(targetTable)) != 3 {
		t.Fatalf("rolled-back INSERT left %d rows, want the original 3",
			len(f.catalog.Rows(targetTable)))
	}
}

// TestWithCheckUpdateFailure makes sure WITH CHECK also guards post-UPDATE
// row state: reassigning the owner away from the caller is rejected.
func TestWithCheckUpdateFailure(t *testing.T) {
	f := newFixture(t)

	stmt := ast.Update(
		ast.Table(targetTable),
		ast.Eq(ast.Col("id"), ast.Lit(int64(1))),
		ast.Set("owner", ast.Lit("mallory")),
	)

	// "owner" is not updatable, so the whitelist fires first; use a policy
	// that permits owner to reach the WITH CHECK path.
	relaxed := policy.Policy{
		Table:            targetTable,
		UpdatableColumns: []string{"status", "name", "owner"},
		Where:            ast.Eq(ast.Col("owner"), ast.Caller()),
		WithCheck:        ast.Eq(ast.Col("owner"), ast.Caller()),
		MaxRows:          policy.UnlimitedRowCount,
	}
	enf, err := policy.NewEnforcer(relaxed, f.catalog)
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	count, err := enf.Enforce(f.ctx, stmt)
	if !errors.Is(err, policy.ErrCheckViolation) {
		t.Fatalf("expected ErrCheckViolation, got count=%d err=%v", count, err)
	}
	if f.catalog.Rows(targetTable)[0]["owner"] != "alice" {
		t.Fatalf("failing UPDATE changed the row: %v", f.catalog.Rows(targetTable)[0])
	}
}

// TestRowLimitInsert rejects a statement whose affected-row count exceeds
// Policy.MaxRows, even when every row satisfies WITH CHECK.
func TestRowLimitInsert(t *testing.T) {
	f := newFixture(t)

	mkRow := func(id int64) []ast.Expr {
		return []ast.Expr{ast.Lit(id), ast.Lit("alice"), ast.Lit("bulk"), ast.Lit("active")}
	}
	stmt := ast.Insert(
		ast.Table(targetTable),
		[]string{"id", "owner", "name", "status"},
		mkRow(20), mkRow(21), mkRow(22),
	)

	count, err := f.enf.Enforce(f.ctx, stmt)
	if !errors.Is(err, policy.ErrRowLimit) {
		t.Fatalf("expected ErrRowLimit, got count=%d err=%v", count, err)
	}
	if len(f.catalog.Rows(targetTable)) != 3 {
		t.Fatalf("over-limit INSERT was not rolled back, rows=%d",
			len(f.catalog.Rows(targetTable)))
	}

	// Exactly at the limit is accepted.
	within := ast.Insert(
		ast.Table(targetTable),
		[]string{"id", "owner", "name", "status"},
		mkRow(20), mkRow(21),
	)
	count, err = f.enf.Enforce(f.ctx, within)
	if err != nil || count != 2 {
		t.Fatalf("limit-boundary insert: count=%d err=%v", count, err)
	}
}

// TestRowLimitDelete applies the same cap to rows matched by a DELETE.
func TestRowLimitDelete(t *testing.T) {
	f := newFixture(t)

	f.catalog.Seed(targetTable,
		policy.Row{"id": int64(4), "owner": "alice", "name": "server-d", "status": "active"},
		policy.Row{"id": int64(5), "owner": "alice", "name": "server-e", "status": "active"},
	)
	stmt := ast.Delete(ast.Table(targetTable),
		ast.Eq(ast.Col("owner"), ast.Lit("alice")))

	count, err := f.enf.Enforce(f.ctx, stmt)
	if !errors.Is(err, policy.ErrRowLimit) {
		t.Fatalf("expected ErrRowLimit, got count=%d err=%v", count, err)
	}
	if len(f.catalog.Rows(targetTable)) != 5 {
		t.Fatalf("over-limit DELETE was not rolled back, rows=%d",
			len(f.catalog.Rows(targetTable)))
	}
}

// TestMultiStatementRejected covers both the explicit batch node and the
// batch entry point: any submission carrying more than one statement fails.
func TestMultiStatementRejected(t *testing.T) {
	f := newFixture(t)

	deleteStmt := ast.Delete(ast.Table(targetTable),
		ast.Eq(ast.Col("id"), ast.Lit(int64(1))))
	insertStmt := ast.Insert(
		ast.Table(targetTable),
		[]string{"id", "owner", "name", "status"},
		[]ast.Expr{ast.Lit(int64(30)), ast.Lit("alice"), ast.Lit("x"), ast.Lit("active")},
	)

	if _, err := f.enf.Enforce(f.ctx, ast.Batch(deleteStmt, insertStmt)); !errors.Is(err, policy.ErrMultiStatement) {
		t.Fatalf("batch node: expected ErrMultiStatement, got %v", err)
	}
	if _, err := f.enf.EnforceBatch(f.ctx, []ast.Stmt{deleteStmt, insertStmt}); !errors.Is(err, policy.ErrMultiStatement) {
		t.Fatalf("EnforceBatch: expected ErrMultiStatement, got %v", err)
	}

	// Single-statement batch must still work through the batch entry point.
	count, err := f.enf.EnforceBatch(f.ctx, []ast.Stmt{deleteStmt})
	if err != nil || count != 1 {
		t.Fatalf("single-statement batch: count=%d err=%v", count, err)
	}
}

// TestSubqueryAndForeignTableRejected exercises the structural denylist.
func TestSubqueryAndForeignTableRejected(t *testing.T) {
	f := newFixture(t)

	subquery := ast.ScalarSubquery(ast.Select(ast.Table(targetTable), nil))
	cases := []struct {
		name string
		stmt ast.Stmt
		want error
	}{
		{
			name: "scalar subquery in WHERE",
			stmt: ast.Delete(ast.Table(targetTable),
				ast.Eq(ast.Col("id"), subquery)),
			want: policy.ErrSubquery,
		},
		{
			name: "foreign table update",
			stmt: ast.Update(ast.Table("other_table"), nil,
				ast.Set("status", ast.Lit("x"))),
			want: policy.ErrForeignTable,
		},
		{
			name: "cross-table column reference",
			stmt: ast.Delete(ast.Table(targetTable),
				ast.Eq(ast.QualifiedCol("other_table", "id"), ast.Lit(int64(1)))),
			want: policy.ErrForeignTable,
		},
		{
			name: "bare select",
			stmt: ast.Select(ast.Table(targetTable), nil),
			want: policy.ErrUnsupportedStatement,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.enf.Enforce(f.ctx, tc.stmt); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}

	if len(f.catalog.Rows(targetTable)) != 3 {
		t.Fatalf("rejected statements mutated the store: %d rows",
			len(f.catalog.Rows(targetTable)))
	}
}

// TestPolicyExpressionRestrictions makes sure controller predicates cannot
// smuggle function calls or subqueries, while columns, literals, caller
// address and block_num remain valid.
func TestPolicyExpressionRestrictions(t *testing.T) {
	base := func() policy.Policy {
		return policy.Policy{
			Table:            targetTable,
			UpdatableColumns: []string{"status"},
		}
	}

	badWhere := base()
	badWhere.Where = ast.Func("evil", ast.Col("owner"))
	if _, err := policy.NewEnforcer(badWhere, policy.NewMemoryCatalog()); !errors.Is(err, policy.ErrPolicyExpression) {
		t.Fatalf("function in policy WHERE: expected ErrPolicyExpression, got %v", err)
	}

	badCheck := base()
	badCheck.WithCheck = ast.ScalarSubquery(ast.Select(ast.Table(targetTable), nil))
	if _, err := policy.NewEnforcer(badCheck, policy.NewMemoryCatalog()); !errors.Is(err, policy.ErrPolicyExpression) {
		t.Fatalf("subquery in WITH CHECK: expected ErrPolicyExpression, got %v", err)
	}

	good := base()
	good.Where = ast.And(
		ast.Eq(ast.Col("owner"), ast.Caller()),
		ast.Gt(ast.Block(), ast.Lit(int64(0))),
	)
	good.WithCheck = ast.Or(
		ast.Eq(ast.Col("status"), ast.Lit("active")),
		ast.NotNull(ast.Col("name")),
	)
	if _, err := policy.NewEnforcer(good, policy.NewMemoryCatalog()); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
}

// TestWhereMergeIsStructuralAndParenthesised verifies the rewrite happens on
// the syntax tree (not via string concatenation) and that the rendered SQL
// groups the entire user predicate in parentheses.
func TestWhereMergeIsStructuralAndParenthesised(t *testing.T) {
	user := ast.Or(
		ast.Eq(ast.Col("name"), ast.Lit("server-a")),
		ast.Eq(ast.Lit(int64(1)), ast.Lit(int64(1))),
	)
	pred := ast.Eq(ast.Col("owner"), ast.Caller())

	merged := policy.MergeWhere(user, pred)
	got := merged.SQL()
	want := `("name" = 'server-a' OR 1 = 1) AND "owner" = caller_addr()`
	if got != want {
		t.Fatalf("merged WHERE mismatch:\n got: %s\nwant: %s", got, want)
	}

	// Identity cases must not invent empty groups.
	if m := policy.MergeWhere(nil, pred); m.SQL() != pred.SQL() {
		t.Fatalf("nil user WHERE should yield policy predicate alone, got %s", m.SQL())
	}
}

// TestInsertHappyPathAndEval checks normal enforcement plumbing: caller_addr
// and block_num evaluation, WITH CHECK success and committed visibility.
func TestInsertHappyPathAndEval(t *testing.T) {
	f := newFixture(t)

	row := []ast.Expr{
		ast.Lit(int64(40)),
		ast.Caller(),
		ast.Lit("block-thing"),
		ast.Lit("active"),
	}
	stmt := ast.Insert(ast.Table(targetTable),
		[]string{"id", "owner", "name", "status"}, row)

	count, err := f.enf.Enforce(f.ctx, stmt)
	if err != nil || count != 1 {
		t.Fatalf("insert: count=%d err=%v", count, err)
	}
	last := f.catalog.Rows(targetTable)[3]
	if last["owner"] != "alice" {
		t.Fatalf("caller_addr() evaluated to %v, want alice", last["owner"])
	}
}
