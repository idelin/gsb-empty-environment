package policy

import (
	"fmt"

	"example.org/netsec/q0509/ast"
)

// MergeWhere AND-combines the policy predicate with the user predicate at the
// syntax-tree level. The user predicate is wrapped in an explicit grouping
// node first, so "OR 1=1" inside it cannot bind looser than the policy AND.
//
// No SQL text is concatenated anywhere in this path: the result is a new AST
// subtree and callers render it only for display.
func MergeWhere(userWhere, policyWhere ast.Expr) ast.Expr {
	switch {
	case policyWhere == nil && userWhere == nil:
		return nil
	case policyWhere == nil:
		return &ast.ParenExpr{Expr: userWhere}
	case userWhere == nil:
		return policyWhere
	default:
		return &ast.BinaryExpr{
			Left:     &ast.ParenExpr{Expr: userWhere},
			Operator: "AND",
			Right:    policyWhere,
		}
	}
}

// enforceRowLimit applies the affected-row cap. The check runs before any
// mutation is published so an over-limit statement fails the whole
// transaction.
func (e *Enforcer) enforceRowLimit(count int) error {
	if e.policy.MaxRows != UnlimitedRowCount && count > e.policy.MaxRows {
		return fmt.Errorf("%w: %d rows affected, limit is %d",
			ErrRowLimit, count, e.policy.MaxRows)
	}
	return nil
}

func (e *Enforcer) executeDelete(ctx Context, txn Txn, stmt *ast.DeleteStmt) (int, error) {
	ev := newEvaluator(ctx, txn.Columns())
	merged := MergeWhere(stmt.Where, e.policy.Where)

	type deletion struct {
		id  int
		row Row
	}
	var matched []deletion
	snapshot := txn.Snapshot()
	for id, row := range snapshot {
		ok, err := ev.isTrue(merged, row)
		if err != nil {
			return 0, err
		}
		if ok {
			matched = append(matched, deletion{id: id, row: row})
		}
	}

	if err := e.enforceRowLimit(len(matched)); err != nil {
		return 0, err
	}

	// Delete in descending id order so earlier ids stay valid throughout.
	for i := len(matched) - 1; i >= 0; i-- {
		if err := txn.Delete(matched[i].id); err != nil {
			return 0, err
		}
	}
	return len(matched), nil
}

func (e *Enforcer) executeUpdate(ctx Context, txn Txn, stmt *ast.UpdateStmt) (int, error) {
	ev := newEvaluator(ctx, txn.Columns())
	merged := MergeWhere(stmt.Where, e.policy.Where)

	type change struct {
		id  int
		row Row
	}
	var matched []change
	snapshot := txn.Snapshot()
	for id, row := range snapshot {
		ok, err := ev.isTrue(merged, row)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}

		next := cloneRow(row)
		for _, assignment := range stmt.Set {
			value, err := ev.eval(assignment.Value, next)
			if err != nil {
				return 0, err
			}
			next[assignment.Column] = value
		}
		if e.policy.WithCheck != nil {
			pass, err := ev.isTrue(e.policy.WithCheck, next)
			if err != nil {
				return 0, err
			}
			if !pass {
				return 0, fmt.Errorf("%w: updated row %v fails check %s",
					ErrCheckViolation, next, e.policy.WithCheck.SQL())
			}
		}
		matched = append(matched, change{id: id, row: next})
	}

	if err := e.enforceRowLimit(len(matched)); err != nil {
		return 0, err
	}

	for _, c := range matched {
		if err := txn.Update(c.id, c.row); err != nil {
			return 0, err
		}
	}
	return len(matched), nil
}

func (e *Enforcer) executeInsert(ctx Context, txn Txn, stmt *ast.InsertStmt) (int, error) {
	columns := txn.Columns()
	ev := newEvaluator(ctx, columns)

	known := make(map[string]struct{}, len(columns))
	for _, c := range columns {
		known[c] = struct{}{}
	}
	for _, c := range stmt.Columns {
		if _, ok := known[c]; !ok {
			return 0, fmt.Errorf("%w: %q", ErrUnknownColumn, c)
		}
	}

	// The row cap applies before any row is evaluated or written, so a large
	// batch cannot partially land before failing.
	if err := e.enforceRowLimit(len(stmt.Rows)); err != nil {
		return 0, err
	}

	inserted := make([]Row, 0, len(stmt.Rows))
	for rowIndex, exprs := range stmt.Rows {
		if len(exprs) != len(stmt.Columns) {
			return 0, fmt.Errorf("%w: row %d has %d values for %d columns",
				ErrBadStatement, rowIndex, len(exprs), len(stmt.Columns))
		}

		row := make(Row, len(columns))
		for _, c := range columns {
			row[c] = nil
		}
		for i, expr := range exprs {
			column := stmt.Columns[i]
			value, err := ev.eval(expr, row)
			if err != nil {
				return 0, fmt.Errorf("column %q: %w", column, err)
			}
			row[column] = value
		}

		// Every new row passes WITH CHECK individually; one failing row fails
		// and rolls back the entire statement.
		if e.policy.WithCheck != nil {
			pass, err := ev.isTrue(e.policy.WithCheck, row)
			if err != nil {
				return 0, err
			}
			if !pass {
				return 0, fmt.Errorf("%w: inserted row %d %v fails check %s",
					ErrCheckViolation, rowIndex, row, e.policy.WithCheck.SQL())
			}
		}
		inserted = append(inserted, row)
	}

	for _, row := range inserted {
		if err := txn.Insert(row); err != nil {
			return 0, err
		}
	}
	return len(inserted), nil
}

func cloneRow(row Row) Row {
	next := make(Row, len(row))
	for k, v := range row {
		next[k] = v
	}
	return next
}
