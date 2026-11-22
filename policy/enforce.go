package policy

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrMultiStatement       = errors.New("policy: multiple statements are not allowed")
	ErrSubquery             = errors.New("policy: subqueries are not allowed")
	ErrCrossTableReference  = errors.New("policy: references to other tables are not allowed")
	ErrColumnNotUpdatable   = errors.New("policy: SET column is not in updatableColumns")
	ErrWithCheckViolation   = errors.New("policy: inserted row violates WITH CHECK")
	ErrRowLimitExceeded     = errors.New("policy: affected rows exceed the policy limit")
	ErrInvalidPolicyExpr    = errors.New("policy: policy expression may only reference columns, literals, caller address and block_num")
	ErrUnsupportedStatement = errors.New("policy: only UPDATE, DELETE and INSERT statements are accepted")
)

// Policy is the controller-defined write policy for one table.
type Policy struct {
	// UpdatableColumns is the allow-list of columns UPDATE may set.
	UpdatableColumns []string
	// Where is AND-merged into the user condition of UPDATE/DELETE.
	Where Expr
	// WithCheck is evaluated against every inserted row.
	WithCheck Expr
	// MaxAffectedRows caps the rows one statement may touch. 0 = unlimited.
	MaxAffectedRows int
}

// Context carries per-transaction values needed at enforcement time.
type Context struct {
	Caller   string
	BlockNum int64
}

// EnforcedStatement is a statement rewritten to satisfy the policy,
// together with the row limit the executor must enforce.
type EnforcedStatement struct {
	Stmt            Statement
	MaxAffectedRows int
}

// NewRowLimiter returns the limiter an executor must feed while
// applying the statement; on error it must roll back.
func (e *EnforcedStatement) NewRowLimiter() *RowLimiter {
	return NewRowLimiter(e.MaxAffectedRows)
}

// Enforce validates the parsed statements of one write request against
// the table policy and returns the rewritten statement. Exactly one
// statement must be supplied; anything else is rejected.
func Enforce(stmts []Statement, table string, pol Policy, ctx Context) (*EnforcedStatement, error) {
	if len(stmts) == 0 {
		return nil, fmt.Errorf("policy: no statement supplied")
	}
	if len(stmts) > 1 {
		return nil, ErrMultiStatement
	}
	if err := validatePolicyExpr(pol.Where); err != nil {
		return nil, err
	}
	if err := validatePolicyExpr(pol.WithCheck); err != nil {
		return nil, err
	}

	enforced := &EnforcedStatement{MaxAffectedRows: pol.MaxAffectedRows}
	switch s := stmts[0].(type) {
	case *UpdateStmt:
		if err := checkTargetTable(s.Table, table); err != nil {
			return nil, err
		}
		if err := checkSetColumns(s.Set, pol.UpdatableColumns); err != nil {
			return nil, err
		}
		for _, c := range s.Set {
			if err := validateUserExpr(c.Value, table); err != nil {
				return nil, err
			}
		}
		if err := validateUserExpr(s.Where, table); err != nil {
			return nil, err
		}
		enforced.Stmt = &UpdateStmt{
			Table: s.Table,
			Set:   s.Set,
			Where: mergeWhere(pol.Where, s.Where),
		}
	case *DeleteStmt:
		if err := checkTargetTable(s.Table, table); err != nil {
			return nil, err
		}
		if err := validateUserExpr(s.Where, table); err != nil {
			return nil, err
		}
		enforced.Stmt = &DeleteStmt{
			Table: s.Table,
			Where: mergeWhere(pol.Where, s.Where),
		}
	case *InsertStmt:
		if err := checkTargetTable(s.Table, table); err != nil {
			return nil, err
		}
		if err := checkInsertRows(s, pol, ctx); err != nil {
			return nil, err
		}
		enforced.Stmt = s
	default:
		return nil, ErrUnsupportedStatement
	}
	return enforced, nil
}

// mergeWhere ANDs the policy condition with the user condition. The user
// condition is wrapped in a ParenExpr node so an injected OR can never
// escape the AND; no string concatenation is involved.
func mergeWhere(policyWhere, userWhere Expr) Expr {
	if policyWhere == nil {
		return userWhere
	}
	if userWhere == nil {
		return policyWhere
	}
	return &BinaryExpr{
		Op:    OpAnd,
		Left:  policyWhere,
		Right: &ParenExpr{X: userWhere},
	}
}

func checkTargetTable(stmtTable, table string) error {
	if !strings.EqualFold(stmtTable, table) {
		return fmt.Errorf("%w: statement targets %q, policy table is %q",
			ErrCrossTableReference, stmtTable, table)
	}
	return nil
}

func checkSetColumns(set []SetClause, updatable []string) error {
	allowed := make(map[string]bool, len(updatable))
	for _, c := range updatable {
		allowed[strings.ToLower(c)] = true
	}
	for _, c := range set {
		if !allowed[strings.ToLower(c.Column)] {
			return fmt.Errorf("%w: %q", ErrColumnNotUpdatable, c.Column)
		}
	}
	return nil
}

// validateUserExpr rejects subqueries and references to other tables
// anywhere inside a user-supplied expression.
func validateUserExpr(e Expr, table string) error {
	return walk(e, func(n Expr) error {
		switch t := n.(type) {
		case *SubqueryExpr:
			return ErrSubquery
		case *ColumnRef:
			if t.Table != "" && !strings.EqualFold(t.Table, table) {
				return fmt.Errorf("%w: %s", ErrCrossTableReference, t.String())
			}
		}
		return nil
	})
}

// validatePolicyExpr restricts policy expressions to columns, literals,
// the caller address and block_num, composed with arithmetic, comparison
// and boolean operators.
func validatePolicyExpr(e Expr) error {
	return walk(e, func(n Expr) error {
		switch n.(type) {
		case *Literal, *ColumnRef, *CallerAddress, *BlockNumber,
			*BinaryExpr, *UnaryExpr, *ParenExpr:
			return nil
		}
		return fmt.Errorf("%w (found %T)", ErrInvalidPolicyExpr, n)
	})
}

// checkInsertRows evaluates WITH CHECK for every row of an INSERT. Any
// failing row fails the whole statement. The row count is also checked
// against the policy limit up front.
func checkInsertRows(s *InsertStmt, pol Policy, ctx Context) error {
	if pol.MaxAffectedRows > 0 && len(s.Rows) > pol.MaxAffectedRows {
		return fmt.Errorf("%w: %d rows inserted, limit is %d",
			ErrRowLimitExceeded, len(s.Rows), pol.MaxAffectedRows)
	}
	if pol.WithCheck == nil {
		return nil
	}
	for i, row := range s.Rows {
		if len(row) != len(s.Columns) {
			return fmt.Errorf("policy: row %d has %d values for %d columns",
				i, len(row), len(s.Columns))
		}
		scope := &Scope{
			Row:      make(map[string]any, len(s.Columns)),
			Caller:   ctx.Caller,
			BlockNum: ctx.BlockNum,
		}
		for j, col := range s.Columns {
			if err := validateUserExpr(row[j], s.Table); err != nil {
				return err
			}
			v, err := Eval(row[j], scope)
			if err != nil {
				return fmt.Errorf("policy: row %d value %q: %w", i, col, err)
			}
			scope.Row[strings.ToLower(col)] = v
		}
		ok, err := boolOf(pol.WithCheck, scope)
		if err != nil {
			return fmt.Errorf("policy: WITH CHECK evaluation failed on row %d: %w", i, err)
		}
		if !ok {
			return fmt.Errorf("%w (row %d)", ErrWithCheckViolation, i)
		}
	}
	return nil
}
