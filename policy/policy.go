// Package policy enforces controller-defined write policies against parsed
// SQL write statements.
//
// Enforcement is structural rather than textual: policy predicates are
// AND-merged into the user's predicate as syntax-tree nodes, with the entire
// user predicate wrapped in an explicit grouping node, so injection payloads
// such as "OR 1=1" cannot widen the row set.
package policy

import (
	"errors"
	"fmt"

	"example.org/netsec/q0509/ast"
)

// Common enforcement failures. Callers can use errors.Is to classify them.
var (
	// ErrMultiStatement is returned when more than one statement is submitted.
	ErrMultiStatement = errors.New("multi-statement submissions are forbidden")
	// ErrSubquery is returned when a statement contains a subquery.
	ErrSubquery = errors.New("subqueries are forbidden")
	// ErrForeignTable is returned when a statement references any table other
	// than the policy's target table.
	ErrForeignTable = errors.New("references to other tables are forbidden")
	// ErrUnsupportedStatement is returned for statement types the enforcer does
	// not accept (for example a bare SELECT).
	ErrUnsupportedStatement = errors.New("unsupported statement for write enforcement")
	// ErrForbiddenColumn is returned when an UPDATE sets a column outside
	// Policy.UpdatableColumns.
	ErrForbiddenColumn = errors.New("column is not updatable under the active policy")
	// ErrPolicyExpression is returned when a policy predicate references
	// anything other than columns, literals, the caller address or block_num.
	ErrPolicyExpression = errors.New("policy expressions may only reference columns, literals, caller address and block_num")
	// ErrCheckViolation is returned when an inserted or updated row fails a
	// WITH CHECK predicate.
	ErrCheckViolation = errors.New("WITH CHECK constraint failed")
	// ErrRowLimit is returned when a statement would affect more rows than
	// Policy.MaxRows allows.
	ErrRowLimit = errors.New("statement affects more rows than the policy limit")
	// ErrUnknownColumn is returned when an expression references a column that
	// does not exist on the target table.
	ErrUnknownColumn = errors.New("unknown column")
	// ErrBadStatement is returned for malformed statements (missing table,
	// arity mismatches and similar structural problems).
	ErrBadStatement = errors.New("malformed write statement")
)

// UnlimitedRowCount marks Policy.MaxRows as having no row cap.
const UnlimitedRowCount = 0

// Policy is the controller-defined write policy for one table.
type Policy struct {
	// Table is the only table the policy permits statements to touch.
	Table string
	// UpdatableColumns is the whitelist of columns an UPDATE SET clause may
	// assign. An empty list forbids all UPDATE assignments.
	UpdatableColumns []string
	// Where is AND-merged into every UPDATE and DELETE predicate. It may only
	// reference columns, literals, the caller address and block_num.
	Where ast.Expr
	// WithCheck is evaluated against every inserted row (and every updated
	// row after applying the SET assignments). A false or NULL result fails
	// and rolls back the whole statement.
	WithCheck ast.Expr
	// MaxRows caps the number of rows a single statement may affect. Zero
	// means unlimited.
	MaxRows int
}

// Context carries the runtime values exposed to policy expressions.
type Context struct {
	// Caller is the transaction caller's address, compared as a string.
	Caller string
	// BlockNum is the block number executing the statement.
	BlockNum int64
}

// Enforcer binds a store to one table policy.
type Enforcer struct {
	policy Policy
	store  Store
}

// NewEnforcer validates the policy expression up front and returns an
// enforcer backed by store.
func NewEnforcer(p Policy, store Store) (*Enforcer, error) {
	if p.Table == "" {
		return nil, fmt.Errorf("%w: policy table name is empty", ErrBadStatement)
	}
	if p.Where != nil {
		if err := validatePolicyExpr(p.Where); err != nil {
			return nil, err
		}
	}
	if p.WithCheck != nil {
		if err := validatePolicyExpr(p.WithCheck); err != nil {
			return nil, err
		}
	}
	if p.MaxRows < 0 {
		return nil, fmt.Errorf("%w: MaxRows must be zero or positive", ErrBadStatement)
	}
	return &Enforcer{policy: p, store: store}, nil
}

// Enforce applies the policy and executes a single statement atomically. On
// any policy failure the transaction is rolled back and the store is left
// unchanged. The returned count is the number of rows affected.
func (e *Enforcer) Enforce(ctx Context, stmt ast.Stmt) (int, error) {
	if stmt == nil {
		return 0, fmt.Errorf("%w: nil statement", ErrBadStatement)
	}
	if err := e.staticCheck(stmt); err != nil {
		return 0, err
	}

	txn, err := e.store.Begin(e.policy.Table)
	if err != nil {
		return 0, err
	}

	count, err := e.execute(ctx, txn, stmt)
	if err != nil {
		txn.Rollback()
		return 0, err
	}
	if err := txn.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// EnforceBatch rejects multi-statement submissions. It exists so the write
// path has one entry point regardless of how the parser grouped input.
func (e *Enforcer) EnforceBatch(ctx Context, stmts []ast.Stmt) (int, error) {
	if len(stmts) > 1 {
		return 0, fmt.Errorf("%w: %d statements submitted", ErrMultiStatement, len(stmts))
	}
	if len(stmts) == 0 {
		return 0, fmt.Errorf("%w: empty statement batch", ErrBadStatement)
	}
	return e.Enforce(ctx, stmts[0])
}

func (e *Enforcer) execute(ctx Context, txn Txn, stmt ast.Stmt) (int, error) {
	switch s := stmt.(type) {
	case *ast.UpdateStmt:
		return e.executeUpdate(ctx, txn, s)
	case *ast.DeleteStmt:
		return e.executeDelete(ctx, txn, s)
	case *ast.InsertStmt:
		return e.executeInsert(ctx, txn, s)
	default:
		return 0, fmt.Errorf("%w: %T", ErrUnsupportedStatement, stmt)
	}
}
