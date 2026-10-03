// Package decorrelator rewrites correlated subqueries (EXISTS / IN) in SQL
// WHERE clauses into regular joins (semi-joins / inner joins), eliminating
// the per-row re-execution of subqueries.
package decorrelator

import (
	"fmt"
	"strings"
)

// SelectStmt is a parsed SELECT statement.
type SelectStmt struct {
	Star    bool
	Columns []SelectItem
	From    []TableRef
	Where   Expr
}

// SelectItem is a single entry in the SELECT list: a column reference
// (Name may be "*" for table.*) or a numeric literal (Literal set).
type SelectItem struct {
	Table   string
	Name    string
	Literal bool
}

// TableRef is a table in the FROM clause. Alias defaults to Name.
type TableRef struct {
	Name  string
	Alias string
}

// Expr is any scalar or boolean expression node.
type Expr interface{ exprNode() }

// ColumnRef references a column, optionally qualified by a table alias.
type ColumnRef struct {
	Table string
	Name  string
}

// NumberLit is a numeric literal.
type NumberLit struct{ Value string }

// StringLit is a single-quoted string literal.
type StringLit struct{ Value string }

// BinaryExpr is a binary operation: comparison (=, <, >, <=, >=, !=, <>)
// or a boolean connective (AND, OR).
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

// ExistsExpr is EXISTS (subquery).
type ExistsExpr struct {
	Subquery *SelectStmt
}

// InExpr is <expr> IN (subquery).
type InExpr struct {
	Left     Expr
	Subquery *SelectStmt
}

func (*ColumnRef) exprNode()  {}
func (*NumberLit) exprNode()  {}
func (*StringLit) exprNode()  {}
func (*BinaryExpr) exprNode() {}
func (*ExistsExpr) exprNode() {}
func (*InExpr) exprNode()     {}

// ExprString renders an expression back to SQL text.
func ExprString(e Expr) string {
	switch v := e.(type) {
	case *ColumnRef:
		if v.Table != "" {
			return v.Table + "." + v.Name
		}
		return v.Name
	case *NumberLit:
		return v.Value
	case *StringLit:
		return "'" + strings.ReplaceAll(v.Value, "'", "''") + "'"
	case *BinaryExpr:
		if v.Op == "AND" || v.Op == "OR" {
			return "(" + ExprString(v.Left) + " " + v.Op + " " + ExprString(v.Right) + ")"
		}
		return ExprString(v.Left) + " " + v.Op + " " + ExprString(v.Right)
	case *ExistsExpr:
		return "EXISTS (...)"
	case *InExpr:
		return ExprString(v.Left) + " IN (...)"
	}
	return fmt.Sprintf("%T", e)
}
