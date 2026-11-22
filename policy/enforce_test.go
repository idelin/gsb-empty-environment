package policy

import (
	"errors"
	"strings"
	"testing"
)

var testCtx = Context{Caller: "0xalice", BlockNum: 100}

func ownerIsCaller() Expr {
	return &BinaryExpr{Op: OpEq, Left: &ColumnRef{Name: "owner"}, Right: &CallerAddress{}}
}

func lit(v any) *Literal { return &Literal{Value: v} }

// memTable is a minimal in-memory table used to simulate execution and
// rollback of enforced statements.
type memTable struct {
	rows []map[string]any
}

func (t *memTable) snapshot() []map[string]any {
	cp := make([]map[string]any, len(t.rows))
	for i, r := range t.rows {
		row := make(map[string]any, len(r))
		for k, v := range r {
			row[k] = v
		}
		cp[i] = row
	}
	return cp
}

// execUpdate applies an enforced UPDATE with row-limit accounting and
// rolls everything back if the limit is exceeded.
func execUpdate(t *memTable, e *EnforcedStatement, ctx Context) (int, error) {
	stmt := e.Stmt.(*UpdateStmt)
	backup := t.snapshot()
	limiter := e.NewRowLimiter()
	affected := 0
	for _, row := range t.rows {
		scope := &Scope{Row: row, Caller: ctx.Caller, BlockNum: ctx.BlockNum}
		match, err := boolOf(stmt.Where, scope)
		if err != nil {
			return 0, err
		}
		if !match {
			continue
		}
		if err := limiter.Add(1); err != nil {
			t.rows = backup
			return 0, err
		}
		for _, c := range stmt.Set {
			v, err := Eval(c.Value, scope)
			if err != nil {
				t.rows = backup
				return 0, err
			}
			row[strings.ToLower(c.Column)] = v
		}
		affected++
	}
	return affected, nil
}

// execDelete applies an enforced DELETE with row-limit accounting and
// rolls everything back if the limit is exceeded.
func execDelete(t *memTable, e *EnforcedStatement, ctx Context) (int, error) {
	stmt := e.Stmt.(*DeleteStmt)
	backup := t.snapshot()
	limiter := e.NewRowLimiter()
	kept := t.rows[:0]
	affected := 0
	for _, row := range t.rows {
		scope := &Scope{Row: row, Caller: ctx.Caller, BlockNum: ctx.BlockNum}
		match, err := boolOf(stmt.Where, scope)
		if err != nil {
			t.rows = backup
			return 0, err
		}
		if !match {
			kept = append(kept, row)
			continue
		}
		if err := limiter.Add(1); err != nil {
			t.rows = backup
			return 0, err
		}
		affected++
	}
	t.rows = kept
	return affected, nil
}

// OR 1=1 must not escape the policy WHERE.
func TestUpdate_OR1eq1_ContainedByPolicy(t *testing.T) {
	pol := Policy{
		UpdatableColumns: []string{"name"},
		Where:            ownerIsCaller(),
		MaxAffectedRows:  10,
	}
	// Attacker tries to update everyone's rows.
	attack := &UpdateStmt{
		Table: "users",
		Set:   []SetClause{{Column: "name", Value: lit("pwned")}},
		Where: &BinaryExpr{
			Op:    OpOr,
			Left:  &BinaryExpr{Op: OpEq, Left: &ColumnRef{Name: "owner"}, Right: lit("0xattacker")},
			Right: &BinaryExpr{Op: OpEq, Left: lit(int64(1)), Right: lit(int64(1))},
		},
	}
	enforced, err := Enforce([]Statement{attack}, "users", pol, testCtx)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}

	// The merged condition must be AND(policy, (user)) at AST level.
	merged := enforced.Stmt.(*UpdateStmt).Where
	and, ok := merged.(*BinaryExpr)
	if !ok || and.Op != OpAnd {
		t.Fatalf("merged WHERE is not an AND node: %T", merged)
	}
	if _, ok := and.Right.(*ParenExpr); !ok {
		t.Fatalf("user condition is not parenthesized: %T", and.Right)
	}
	want := "((owner = $caller) AND (((owner = '0xattacker') OR (1 = 1))))"
	if merged.String() != want {
		t.Fatalf("merged WHERE = %s, want %s", merged.String(), want)
	}

	table := &memTable{rows: []map[string]any{
		{"owner": "0xalice", "name": "alice"},
		{"owner": "0xbob", "name": "bob"},
		{"owner": "0xattacker", "name": "mallory"},
	}}
	affected, err := execUpdate(table, enforced, testCtx)
	if err != nil {
		t.Fatalf("execUpdate: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected = %d, want 1 (only the caller's row)", affected)
	}
	if table.rows[1]["name"] != "bob" || table.rows[2]["name"] != "mallory" {
		t.Fatalf("OR 1=1 leaked: rows = %v", table.rows)
	}
	if table.rows[0]["name"] != "pwned" {
		t.Fatalf("caller row not updated: %v", table.rows[0])
	}
}

// DELETE with OR 1=1 must likewise stay inside the policy WHERE.
func TestDelete_OR1eq1_ContainedByPolicy(t *testing.T) {
	pol := Policy{Where: ownerIsCaller()}
	attack := &DeleteStmt{
		Table: "users",
		Where: &BinaryExpr{Op: OpOr,
			Left:  &BinaryExpr{Op: OpEq, Left: &ColumnRef{Name: "owner"}, Right: lit("0xattacker")},
			Right: &BinaryExpr{Op: OpEq, Left: lit(int64(1)), Right: lit(int64(1))}},
	}
	enforced, err := Enforce([]Statement{attack}, "users", pol, testCtx)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	table := &memTable{rows: []map[string]any{
		{"owner": "0xalice"},
		{"owner": "0xbob"},
	}}
	affected, err := execDelete(table, enforced, testCtx)
	if err != nil {
		t.Fatalf("execDelete: %v", err)
	}
	if affected != 1 || len(table.rows) != 1 || table.rows[0]["owner"] != "0xbob" {
		t.Fatalf("OR 1=1 leaked: affected=%d rows=%v", affected, table.rows)
	}
}

// SET outside updatableColumns must be rejected.
func TestUpdate_PrivilegedSetColumn_Rejected(t *testing.T) {
	pol := Policy{
		UpdatableColumns: []string{"name"},
		Where:            ownerIsCaller(),
	}
	attack := &UpdateStmt{
		Table: "users",
		Set: []SetClause{
			{Column: "name", Value: lit("x")},
			{Column: "owner", Value: lit("0xattacker")}, // privilege escalation
		},
		Where: &BinaryExpr{Op: OpEq, Left: &ColumnRef{Name: "owner"}, Right: lit("0xattacker")},
	}
	_, err := Enforce([]Statement{attack}, "users", pol, testCtx)
	if !errors.Is(err, ErrColumnNotUpdatable) {
		t.Fatalf("err = %v, want ErrColumnNotUpdatable", err)
	}
}

// An INSERT row failing WITH CHECK fails the whole statement.
func TestInsert_WithCheckViolation_WholeStatementFails(t *testing.T) {
	pol := Policy{
		WithCheck: &BinaryExpr{Op: OpGte, Left: &ColumnRef{Name: "balance"}, Right: lit(int64(0))},
	}
	stmt := &InsertStmt{
		Table:   "accounts",
		Columns: []string{"owner", "balance"},
		Rows: [][]Expr{
			{lit("0xalice"), lit(int64(10))},
			{lit("0xmallory"), lit(int64(-5))}, // violates balance >= 0
		},
	}
	_, err := Enforce([]Statement{stmt}, "accounts", pol, testCtx)
	if !errors.Is(err, ErrWithCheckViolation) {
		t.Fatalf("err = %v, want ErrWithCheckViolation", err)
	}

	// Same statement without the offending row passes.
	stmt.Rows = stmt.Rows[:1]
	if _, err := Enforce([]Statement{stmt}, "accounts", pol, testCtx); err != nil {
		t.Fatalf("Enforce with valid rows: %v", err)
	}
}

// WITH CHECK may reference the caller address and block_num.
func TestInsert_WithCheckCallerAndBlockNum(t *testing.T) {
	pol := Policy{
		WithCheck: &BinaryExpr{Op: OpAnd,
			Left:  &BinaryExpr{Op: OpEq, Left: &ColumnRef{Name: "owner"}, Right: &CallerAddress{}},
			Right: &BinaryExpr{Op: OpLte, Left: &ColumnRef{Name: "created_at"}, Right: &BlockNumber{}}},
	}
	stmt := &InsertStmt{
		Table:   "accounts",
		Columns: []string{"owner", "created_at"},
		Rows:    [][]Expr{{&CallerAddress{}, lit(int64(99))}},
	}
	if _, err := Enforce([]Statement{stmt}, "accounts", pol, testCtx); err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	// Row owned by someone else must fail.
	stmt.Rows = [][]Expr{{lit("0xmallory"), lit(int64(99))}}
	if _, err := Enforce([]Statement{stmt}, "accounts", pol, testCtx); !errors.Is(err, ErrWithCheckViolation) {
		t.Fatalf("err = %v, want ErrWithCheckViolation", err)
	}
}

// A statement touching more rows than the limit must roll back.
func TestDelete_RowLimitExceeded_RollsBack(t *testing.T) {
	pol := Policy{
		Where:           ownerIsCaller(),
		MaxAffectedRows: 2,
	}
	stmt := &DeleteStmt{
		Table: "users",
		Where: &BinaryExpr{Op: OpEq, Left: lit(int64(1)), Right: lit(int64(1))},
	}
	enforced, err := Enforce([]Statement{stmt}, "users", pol, testCtx)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	table := &memTable{rows: []map[string]any{
		{"owner": "0xalice", "id": int64(1)},
		{"owner": "0xalice", "id": int64(2)},
		{"owner": "0xalice", "id": int64(3)},
		{"owner": "0xbob", "id": int64(4)},
	}}
	_, err = execDelete(table, enforced, testCtx)
	if !errors.Is(err, ErrRowLimitExceeded) {
		t.Fatalf("err = %v, want ErrRowLimitExceeded", err)
	}
	if len(table.rows) != 4 {
		t.Fatalf("rollback failed, %d rows remain: %v", len(table.rows), table.rows)
	}
}

// INSERT with more rows than the limit is rejected up front.
func TestInsert_RowLimitExceeded(t *testing.T) {
	pol := Policy{MaxAffectedRows: 1}
	stmt := &InsertStmt{
		Table:   "accounts",
		Columns: []string{"owner"},
		Rows:    [][]Expr{{lit("0xalice")}, {lit("0xbob")}},
	}
	if _, err := Enforce([]Statement{stmt}, "accounts", pol, testCtx); !errors.Is(err, ErrRowLimitExceeded) {
		t.Fatalf("err = %v, want ErrRowLimitExceeded", err)
	}
}

// Stacked statements are rejected outright.
func TestMultiStatement_Rejected(t *testing.T) {
	pol := Policy{Where: ownerIsCaller()}
	// "UPDATE ...; DELETE FROM users" arrives as two parsed statements.
	stmts := []Statement{
		&UpdateStmt{Table: "users", Set: []SetClause{{Column: "name", Value: lit("x")}}},
		&DeleteStmt{Table: "users"},
	}
	if _, err := Enforce(stmts, "users", pol, testCtx); !errors.Is(err, ErrMultiStatement) {
		t.Fatalf("err = %v, want ErrMultiStatement", err)
	}
}

// Subqueries anywhere in the statement are rejected.
func TestSubquery_Rejected(t *testing.T) {
	pol := Policy{Where: ownerIsCaller()}
	stmt := &DeleteStmt{
		Table: "users",
		Where: &BinaryExpr{Op: OpEq,
			Left:  &ColumnRef{Name: "owner"},
			Right: &SubqueryExpr{Query: &SelectStmt{Table: "admins"}},
		},
	}
	if _, err := Enforce([]Statement{stmt}, "users", pol, testCtx); !errors.Is(err, ErrSubquery) {
		t.Fatalf("err = %v, want ErrSubquery", err)
	}
}

// References to other tables are rejected.
func TestCrossTableReference_Rejected(t *testing.T) {
	pol := Policy{UpdatableColumns: []string{"name"}, Where: ownerIsCaller()}
	stmt := &UpdateStmt{
		Table: "users",
		Set:   []SetClause{{Column: "name", Value: lit("x")}},
		Where: &BinaryExpr{Op: OpEq,
			Left:  &ColumnRef{Table: "secrets", Name: "key"},
			Right: lit("k")},
	}
	if _, err := Enforce([]Statement{stmt}, "users", pol, testCtx); !errors.Is(err, ErrCrossTableReference) {
		t.Fatalf("err = %v, want ErrCrossTableReference", err)
	}
}

// Policy expressions may only use columns, literals, caller, block_num.
func TestPolicyExpr_RestrictedVocabulary(t *testing.T) {
	bad := Policy{
		Where: &BinaryExpr{Op: OpEq,
			Left:  &ColumnRef{Name: "owner"},
			Right: &FuncCall{Name: "current_user"}},
	}
	stmt := &DeleteStmt{Table: "users", Where: &BinaryExpr{Op: OpEq, Left: lit(int64(1)), Right: lit(int64(1))}}
	if _, err := Enforce([]Statement{stmt}, "users", bad, testCtx); !errors.Is(err, ErrInvalidPolicyExpr) {
		t.Fatalf("err = %v, want ErrInvalidPolicyExpr", err)
	}

	badCheck := Policy{WithCheck: &SubqueryExpr{Query: &SelectStmt{Table: "t"}}}
	ins := &InsertStmt{Table: "users", Columns: []string{"a"}, Rows: [][]Expr{{lit(int64(1))}}}
	if _, err := Enforce([]Statement{ins}, "users", badCheck, testCtx); !errors.Is(err, ErrInvalidPolicyExpr) {
		t.Fatalf("err = %v, want ErrInvalidPolicyExpr", err)
	}
}

// Non-write statements are not accepted by the enforcer.
func TestSelectStatement_Rejected(t *testing.T) {
	if _, err := Enforce([]Statement{&SelectStmt{Table: "users"}}, "users", Policy{}, testCtx); !errors.Is(err, ErrUnsupportedStatement) {
		t.Fatalf("err = %v, want ErrUnsupportedStatement", err)
	}
}
