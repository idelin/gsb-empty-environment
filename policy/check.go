package policy

import (
	"fmt"

	"example.org/netsec/q0509/ast"
)

// staticCheck performs every policy check that does not require row data.
func (e *Enforcer) staticCheck(stmt ast.Stmt) error {
	if multi, ok := stmt.(*ast.MultiStmt); ok {
		return fmt.Errorf("%w: %d statements submitted", ErrMultiStatement, len(multi.Stmts))
	}

	table, err := targetTable(stmt)
	if err != nil {
		return err
	}
	if table.Name != e.policy.Table {
		return fmt.Errorf("%w: statement targets %q, policy governs %q",
			ErrForeignTable, table.Name, e.policy.Table)
	}

	var rejected error
	ast.Inspect(stmt, func(node ast.Node) bool {
		if rejected != nil {
			return false
		}
		switch n := node.(type) {
		case *ast.SubqueryExpr:
			rejected = fmt.Errorf("%w: scalar subquery in %s", ErrSubquery, stmt.SQL())
			return false
		case *ast.SelectStmt:
			rejected = fmt.Errorf("%w: SELECT is not a write statement", ErrUnsupportedStatement)
			return false
		case *ast.TableRef:
			if n.Name != e.policy.Table {
				rejected = fmt.Errorf("%w: %q referenced by %s",
					ErrForeignTable, n.Name, stmt.SQL())
				return false
			}
		case *ast.Identifier:
			if n.Table != "" && n.Table != table.Name && n.Table != table.Alias {
				rejected = fmt.Errorf("%w: qualifier %q does not name the target table",
					ErrForeignTable, n.Table)
				return false
			}
		}
		return true
	})
	if rejected != nil {
		return rejected
	}

	if update, ok := stmt.(*ast.UpdateStmt); ok {
		if err := e.checkUpdatableColumns(update); err != nil {
			return err
		}
	}
	return nil
}

func targetTable(stmt ast.Stmt) (*ast.TableRef, error) {
	switch s := stmt.(type) {
	case *ast.UpdateStmt:
		if s.Table == nil {
			return nil, fmt.Errorf("%w: UPDATE without a target table", ErrBadStatement)
		}
		return s.Table, nil
	case *ast.DeleteStmt:
		if s.Table == nil {
			return nil, fmt.Errorf("%w: DELETE without a target table", ErrBadStatement)
		}
		return s.Table, nil
	case *ast.InsertStmt:
		if s.Table == nil {
			return nil, fmt.Errorf("%w: INSERT without a target table", ErrBadStatement)
		}
		return s.Table, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedStatement, stmt)
	}
}

func (e *Enforcer) checkUpdatableColumns(update *ast.UpdateStmt) error {
	if len(update.Set) == 0 {
		return fmt.Errorf("%w: UPDATE has no SET clause", ErrBadStatement)
	}
	allowed := make(map[string]struct{}, len(e.policy.UpdatableColumns))
	for _, c := range e.policy.UpdatableColumns {
		allowed[c] = struct{}{}
	}
	seen := make(map[string]struct{}, len(update.Set))
	for _, assignment := range update.Set {
		if assignment == nil || assignment.Column == "" {
			return fmt.Errorf("%w: empty SET target", ErrBadStatement)
		}
		if _, ok := seen[assignment.Column]; ok {
			return fmt.Errorf("%w: column %q assigned twice", ErrBadStatement, assignment.Column)
		}
		seen[assignment.Column] = struct{}{}
		if _, ok := allowed[assignment.Column]; !ok {
			return fmt.Errorf("%w: %q is not in the updatable column list",
				ErrForbiddenColumn, assignment.Column)
		}
	}
	return nil
}

// validatePolicyExpr enforces that policy predicates may only mention
// columns, literals, the caller address and block_num, combined through
// ordinary operators. Function calls, subqueries and other dynamic nodes are
// rejected.
func validatePolicyExpr(expr ast.Expr) error {
	if expr == nil {
		return fmt.Errorf("%w: nil policy expression", ErrPolicyExpression)
	}
	var rejected error
	ast.Inspect(expr, func(node ast.Node) bool {
		if rejected != nil {
			return false
		}
		switch node.(type) {
		case *ast.Literal, *ast.Identifier, *ast.CallerAddr, *ast.BlockNum,
			*ast.BinaryExpr, *ast.UnaryExpr, *ast.ParenExpr, *ast.IsNull, *ast.InExpr:
			return true
		default:
			rejected = fmt.Errorf("%w: forbidden node %T in policy predicate",
				ErrPolicyExpression, node)
			return false
		}
	})
	return rejected
}
