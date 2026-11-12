// Package ast defines the parsed SQL syntax tree that the policy enforcement
// layer consumes.
//
// The enforcement package never reconstructs SQL by concatenating user text.
// It rewrites statements by building new tree nodes here and renders the
// canonical text with [Node.SQL] only for logging and assertion purposes.
package ast

// Node is implemented by every AST node.
type Node interface {
	// SQL returns the canonical SQL rendering of the node.
	SQL() string
	node()
}

// Expr is a scalar or boolean expression node.
type Expr interface {
	Node
	expr()
}

// Stmt is a single SQL statement node.
type Stmt interface {
	Node
	stmt()
}

// TableRef names a table. An empty Alias means the table itself.
type TableRef struct {
	Name  string
	Alias string
}

func (t *TableRef) node() {}

// SQL renders the table reference.
func (t *TableRef) SQL() string {
	if t.Alias != "" {
		return quoteIdent(t.Name) + " AS " + quoteIdent(t.Alias)
	}
	return quoteIdent(t.Name)
}

// Literal is a typed literal value: int64, float64, string, bool or nil.
type Literal struct {
	Value any
}

func (*Literal) node() {}
func (*Literal) expr() {}

// SQL renders the literal.
func (l *Literal) SQL() string { return renderLiteral(l.Value) }

// Identifier is a bare column reference, optionally qualified with a table
// name or alias.
type Identifier struct {
	Table string
	Name  string
}

func (*Identifier) node() {}
func (*Identifier) expr() {}

// SQL renders the column reference.
func (l *Identifier) SQL() string {
	if l.Table != "" {
		return quoteIdent(l.Table) + "." + quoteIdent(l.Name)
	}
	return quoteIdent(l.Name)
}

// CallerAddr evaluates to the transaction caller's address.
type CallerAddr struct{}

func (*CallerAddr) node() {}
func (*CallerAddr) expr() {}

// SQL renders the caller-address pseudo column.
func (*CallerAddr) SQL() string { return "caller_addr()" }

// BlockNum evaluates to the block number executing the statement.
type BlockNum struct{}

func (*BlockNum) node() {}
func (*BlockNum) expr() {}

// SQL renders the block-number pseudo column.
func (*BlockNum) SQL() string { return "block_num()" }

// BinaryExpr is an infix operation such as "a = 1" or "x OR y".
type BinaryExpr struct {
	Left     Expr
	Operator string
	Right    Expr
}

func (*BinaryExpr) node() {}
func (*BinaryExpr) expr() {}

// SQL renders the infix expression.
func (b *BinaryExpr) SQL() string {
	return b.Left.SQL() + " " + b.Operator + " " + b.Right.SQL()
}

// UnaryExpr is a prefix operation such as "NOT x" or "-y".
type UnaryExpr struct {
	Operator string
	Expr     Expr
}

func (*UnaryExpr) node() {}
func (*UnaryExpr) expr() {}

// SQL renders the prefix expression.
func (u *UnaryExpr) SQL() string {
	return u.Operator + " " + u.Expr.SQL()
}

// ParenExpr forces grouping around an expression. The enforcer wraps every
// user-supplied condition in a ParenExpr before AND-merging the policy
// predicate, so a user "OR 1=1" can never escape its scope.
type ParenExpr struct {
	Expr Expr
}

func (*ParenExpr) node() {}
func (*ParenExpr) expr() {}

// SQL renders the parenthesised expression.
func (p *ParenExpr) SQL() string { return "(" + p.Expr.SQL() + ")" }

// IsNull renders "x IS NULL" or "x IS NOT NULL".
type IsNull struct {
	Expr Expr
	Not  bool
}

func (*IsNull) node() {}
func (*IsNull) expr() {}

// SQL renders the null test.
func (i *IsNull) SQL() string {
	if i.Not {
		return i.Expr.SQL() + " IS NOT NULL"
	}
	return i.Expr.SQL() + " IS NULL"
}

// InExpr renders "x IN (a, b, c)" or "x NOT IN (...)".
type InExpr struct {
	Expr   Expr
	Not    bool
	Values []Expr
}

func (*InExpr) node() {}
func (*InExpr) expr() {}

// SQL renders the IN predicate.
func (e *InExpr) SQL() string {
	sql := e.Expr.SQL()
	if e.Not {
		sql += " NOT IN ("
	} else {
		sql += " IN ("
	}
	for i, v := range e.Values {
		if i > 0 {
			sql += ", "
		}
		sql += v.SQL()
	}
	return sql + ")"
}

// FuncExpr is a scalar function call such as "lower(name)".
type FuncExpr struct {
	Name string
	Args []Expr
}

func (*FuncExpr) node() {}
func (*FuncExpr) expr() {}

// SQL renders the function call.
func (f *FuncExpr) SQL() string {
	sql := f.Name + "("
	for i, a := range f.Args {
		if i > 0 {
			sql += ", "
		}
		sql += a.SQL()
	}
	return sql + ")"
}

// SubqueryExpr wraps a SELECT used as a scalar expression. The enforcer
// rejects every statement containing one.
type SubqueryExpr struct {
	Select *SelectStmt
}

func (*SubqueryExpr) node() {}
func (*SubqueryExpr) expr() {}

// SQL renders the scalar subquery.
func (s *SubqueryExpr) SQL() string { return "(" + s.Select.SQL() + ")" }

// SelectStmt is a SELECT statement. It exists so subqueries and stray reads
// can be detected; the write enforcer rejects all of them.
type SelectStmt struct {
	From  *TableRef
	Where Expr
}

func (*SelectStmt) node() {}
func (*SelectStmt) stmt() {}

// SQL renders the SELECT statement.
func (s *SelectStmt) SQL() string {
	sql := "SELECT * FROM " + s.From.SQL()
	if s.Where != nil {
		sql += " WHERE " + s.Where.SQL()
	}
	return sql
}

// Assignment binds a target column to the expression computing its new value.
type Assignment struct {
	Column string
	Value  Expr
}

// SQL renders the SET clause element.
func (a *Assignment) SQL() string {
	return quoteIdent(a.Column) + " = " + a.Value.SQL()
}

// UpdateStmt is an UPDATE statement.
type UpdateStmt struct {
	Table *TableRef
	Set   []*Assignment
	Where Expr
}

func (*UpdateStmt) node() {}
func (*UpdateStmt) stmt() {}

// SQL renders the UPDATE statement.
func (u *UpdateStmt) SQL() string {
	sql := "UPDATE " + u.Table.SQL() + " SET "
	for i, a := range u.Set {
		if i > 0 {
			sql += ", "
		}
		sql += a.SQL()
	}
	if u.Where != nil {
		sql += " WHERE " + u.Where.SQL()
	}
	return sql
}

// DeleteStmt is a DELETE statement.
type DeleteStmt struct {
	Table *TableRef
	Where Expr
}

func (*DeleteStmt) node() {}
func (*DeleteStmt) stmt() {}

// SQL renders the DELETE statement.
func (d *DeleteStmt) SQL() string {
	sql := "DELETE FROM " + d.Table.SQL()
	if d.Where != nil {
		sql += " WHERE " + d.Where.SQL()
	}
	return sql
}

// InsertStmt inserts one or more fully-specified rows.
type InsertStmt struct {
	Table   *TableRef
	Columns []string
	Rows    [][]Expr
}

func (*InsertStmt) node() {}
func (*InsertStmt) stmt() {}

// SQL renders the INSERT statement.
func (i *InsertStmt) SQL() string {
	sql := "INSERT INTO " + i.Table.SQL() + " ("
	for j, c := range i.Columns {
		if j > 0 {
			sql += ", "
		}
		sql += quoteIdent(c)
	}
	sql += ") VALUES "
	for r, row := range i.Rows {
		if r > 0 {
			sql += ", "
		}
		sql += "("
		for j, v := range row {
			if j > 0 {
				sql += ", "
			}
			sql += v.SQL()
		}
		sql += ")"
	}
	return sql
}

// MultiStmt is a batch of statements submitted together. The enforcer rejects
// any submission containing more than one statement.
type MultiStmt struct {
	Stmts []Stmt
}

func (*MultiStmt) node() {}
func (*MultiStmt) stmt() {}

// SQL renders the statement batch.
func (m *MultiStmt) SQL() string {
	sql := ""
	for i, s := range m.Stmts {
		if i > 0 {
			sql += "; "
		}
		sql += s.SQL()
	}
	return sql
}
