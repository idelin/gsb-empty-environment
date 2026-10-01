// Package logplan implements a minimal relational-algebra logical plan
// toolkit: a SQL core-subset parser, an AST-to-logical-plan converter,
// predicate pushdown / projection elimination rules, and tree printing.
package logplan

// Expr is any scalar expression in the SQL AST.
type Expr interface {
	exprNode()
	String() string
}

// ColumnRef references a column, optionally qualified by a table alias.
type ColumnRef struct {
	Table string // may be empty when unqualified
	Name  string
}

func (*ColumnRef) exprNode() {}

func (c *ColumnRef) String() string {
	if c.Table != "" {
		return c.Table + "." + c.Name
	}
	return c.Name
}

// Literal is a constant value (number or single-quoted string).
type Literal struct {
	Value string
}

func (*Literal) exprNode() {}

func (l *Literal) String() string { return l.Value }

// BinaryExpr is a binary operation: comparisons (=, >, <, >=, <=, !=)
// and logical connectives (AND, OR).
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (*BinaryExpr) exprNode() {}

func (b *BinaryExpr) String() string {
	return b.Left.String() + " " + b.Op + " " + b.Right.String()
}

// TableRef is a table with an optional alias in the FROM clause.
type TableRef struct {
	Name  string
	Alias string // falls back to Name when empty
}

// EffectiveAlias returns the alias if set, otherwise the table name.
func (t *TableRef) EffectiveAlias() string {
	if t.Alias != "" {
		return t.Alias
	}
	return t.Name
}

// JoinClause is one JOIN ... ON ... step.
type JoinClause struct {
	Table *TableRef
	On    Expr
}

// SelectStmt is the core SELECT AST:
//
//	SELECT <items> FROM <table> [JOIN <table> ON <expr>]* [WHERE <expr>]
type SelectStmt struct {
	Items []Expr // projection list; Star is true when it is "*"
	Star  bool
	From  *TableRef
	Joins []*JoinClause
	Where Expr // nil when absent
}
