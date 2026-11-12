package ast

// This file provides small constructors so callers (and tests) can build
// parsed trees without taking addresses of temporary values.

// Lit builds a literal node.
func Lit(value any) *Literal { return &Literal{Value: value} }

// Col builds an unqualified column reference.
func Col(name string) *Identifier { return &Identifier{Name: name} }

// QualifiedCol builds a table-qualified column reference.
func QualifiedCol(table, name string) *Identifier {
	return &Identifier{Table: table, Name: name}
}

// Caller builds a caller-address pseudo column.
func Caller() *CallerAddr { return &CallerAddr{} }

// Block builds a block-number pseudo column.
func Block() *BlockNum { return &BlockNum{} }

// Bin builds a binary expression.
func Bin(op string, left, right Expr) *BinaryExpr {
	return &BinaryExpr{Left: left, Operator: op, Right: right}
}

// Not builds a NOT expression.
func Not(expr Expr) *UnaryExpr { return &UnaryExpr{Operator: "NOT", Expr: expr} }

// Neg builds a unary minus expression.
func Neg(expr Expr) *UnaryExpr { return &UnaryExpr{Operator: "-", Expr: expr} }

// Paren wraps an expression in an explicit grouping node.
func Paren(expr Expr) *ParenExpr { return &ParenExpr{Expr: expr} }

// Null builds an IS NULL predicate.
func Null(expr Expr) *IsNull { return &IsNull{Expr: expr} }

// NotNull builds an IS NOT NULL predicate.
func NotNull(expr Expr) *IsNull { return &IsNull{Expr: expr, Not: true} }

// In builds an IN predicate.
func In(expr Expr, values ...Expr) *InExpr {
	return &InExpr{Expr: expr, Values: values}
}

// NotIn builds a NOT IN predicate.
func NotIn(expr Expr, values ...Expr) *InExpr {
	return &InExpr{Expr: expr, Not: true, Values: values}
}

// Func builds a scalar function call.
func Func(name string, args ...Expr) *FuncExpr {
	return &FuncExpr{Name: name, Args: args}
}

// Eq builds "left = right".
func Eq(left, right Expr) *BinaryExpr { return Bin("=", left, right) }

// Neq builds "left <> right".
func Neq(left, right Expr) *BinaryExpr { return Bin("<>", left, right) }

// Gt builds "left > right".
func Gt(left, right Expr) *BinaryExpr { return Bin(">", left, right) }

// Gte builds "left >= right".
func Gte(left, right Expr) *BinaryExpr { return Bin(">=", left, right) }

// Lt builds "left < right".
func Lt(left, right Expr) *BinaryExpr { return Bin("<", left, right) }

// Lte builds "left <= right".
func Lte(left, right Expr) *BinaryExpr { return Bin("<=", left, right) }

// And builds "left AND right".
func And(left, right Expr) *BinaryExpr { return Bin("AND", left, right) }

// Or builds "left OR right".
func Or(left, right Expr) *BinaryExpr { return Bin("OR", left, right) }

// Table builds a table reference.
func Table(name string) *TableRef { return &TableRef{Name: name} }

// Set builds a SET assignment.
func Set(column string, value Expr) *Assignment {
	return &Assignment{Column: column, Value: value}
}

// Update builds an UPDATE statement.
func Update(table *TableRef, where Expr, set ...*Assignment) *UpdateStmt {
	return &UpdateStmt{Table: table, Set: set, Where: where}
}

// Delete builds a DELETE statement.
func Delete(table *TableRef, where Expr) *DeleteStmt {
	return &DeleteStmt{Table: table, Where: where}
}

// Insert builds an INSERT statement.
func Insert(table *TableRef, columns []string, rows ...[]Expr) *InsertStmt {
	return &InsertStmt{Table: table, Columns: columns, Rows: rows}
}

// Batch builds a multi-statement batch.
func Batch(stmts ...Stmt) *MultiStmt { return &MultiStmt{Stmts: stmts} }

// Select builds a SELECT statement.
func Select(from *TableRef, where Expr) *SelectStmt {
	return &SelectStmt{From: from, Where: where}
}

// ScalarSubquery wraps a SELECT as a scalar expression.
func ScalarSubquery(selectStmt *SelectStmt) *SubqueryExpr {
	return &SubqueryExpr{Select: selectStmt}
}

// Inspect walks an AST depth-first, invoking visit for every node. Traversal
// stops if visit returns false.
func Inspect(node Node, visit func(Node) bool) {
	if node == nil || !visit(node) {
		return
	}
	switch n := node.(type) {
	case *TableRef:
	case *Literal, *CallerAddr, *BlockNum:
	case *Identifier:
	case *BinaryExpr:
		Inspect(n.Left, visit)
		Inspect(n.Right, visit)
	case *UnaryExpr:
		Inspect(n.Expr, visit)
	case *ParenExpr:
		Inspect(n.Expr, visit)
	case *IsNull:
		Inspect(n.Expr, visit)
	case *InExpr:
		Inspect(n.Expr, visit)
		for _, v := range n.Values {
			Inspect(v, visit)
		}
	case *FuncExpr:
		for _, a := range n.Args {
			Inspect(a, visit)
		}
	case *SubqueryExpr:
		Inspect(n.Select, visit)
	case *SelectStmt:
		Inspect(n.From, visit)
		Inspect(n.Where, visit)
	case *UpdateStmt:
		Inspect(n.Table, visit)
		for _, a := range n.Set {
			Inspect(a.Value, visit)
		}
		Inspect(n.Where, visit)
	case *DeleteStmt:
		Inspect(n.Table, visit)
		Inspect(n.Where, visit)
	case *InsertStmt:
		Inspect(n.Table, visit)
		for _, row := range n.Rows {
			for _, v := range row {
				Inspect(v, visit)
			}
		}
	case *MultiStmt:
		for _, s := range n.Stmts {
			Inspect(s, visit)
		}
	}
}
