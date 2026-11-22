package policy

import (
	"fmt"
	"strings"
)

// BinOp is a binary operator in an expression.
type BinOp int

const (
	OpAnd BinOp = iota
	OpOr
	OpEq
	OpNeq
	OpLt
	OpLte
	OpGt
	OpGte
	OpAdd
	OpSub
	OpMul
	OpDiv
)

func (o BinOp) String() string {
	switch o {
	case OpAnd:
		return "AND"
	case OpOr:
		return "OR"
	case OpEq:
		return "="
	case OpNeq:
		return "!="
	case OpLt:
		return "<"
	case OpLte:
		return "<="
	case OpGt:
		return ">"
	case OpGte:
		return ">="
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	}
	return "?"
}

// UnOp is a unary operator in an expression.
type UnOp int

const (
	OpNot UnOp = iota
	OpNeg
)

func (o UnOp) String() string {
	if o == OpNot {
		return "NOT "
	}
	return "-"
}

// Expr is a node in a parsed SQL expression tree.
type Expr interface {
	exprNode()
	String() string
}

// ColumnRef references a column, optionally qualified by a table name.
type ColumnRef struct {
	Table string
	Name  string
}

func (*ColumnRef) exprNode() {}

func (c *ColumnRef) String() string {
	if c.Table != "" {
		return c.Table + "." + c.Name
	}
	return c.Name
}

// Literal is a constant value: nil, bool, int64, float64 or string.
type Literal struct {
	Value any
}

func (*Literal) exprNode() {}

func (l *Literal) String() string {
	switch v := l.Value.(type) {
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case nil:
		return "NULL"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// CallerAddress evaluates to the transaction caller's address.
type CallerAddress struct{}

func (*CallerAddress) exprNode()      {}
func (*CallerAddress) String() string { return "$caller" }

// BlockNumber evaluates to the current block number.
type BlockNumber struct{}

func (*BlockNumber) exprNode()      {}
func (*BlockNumber) String() string { return "$block_num" }

// BinaryExpr is a binary operation.
type BinaryExpr struct {
	Op    BinOp
	Left  Expr
	Right Expr
}

func (*BinaryExpr) exprNode() {}

func (b *BinaryExpr) String() string {
	return "(" + b.Left.String() + " " + b.Op.String() + " " + b.Right.String() + ")"
}

// UnaryExpr is a unary operation.
type UnaryExpr struct {
	Op UnOp
	X  Expr
}

func (*UnaryExpr) exprNode() {}

func (u *UnaryExpr) String() string { return u.Op.String() + u.X.String() }

// ParenExpr explicitly parenthesizes an expression. It is used when
// merging a user-supplied condition into a policy condition so operator
// precedence can never leak the user's OR out of the policy AND.
type ParenExpr struct {
	X Expr
}

func (*ParenExpr) exprNode() {}

func (p *ParenExpr) String() string { return "(" + p.X.String() + ")" }

// FuncCall is a function invocation. It is never allowed inside policy
// expressions.
type FuncCall struct {
	Name string
	Args []Expr
}

func (*FuncCall) exprNode() {}

func (f *FuncCall) String() string {
	args := make([]string, len(f.Args))
	for i, a := range f.Args {
		args[i] = a.String()
	}
	return f.Name + "(" + strings.Join(args, ", ") + ")"
}

// SubqueryExpr embeds a SELECT inside an expression. Always rejected.
type SubqueryExpr struct {
	Query *SelectStmt
}

func (*SubqueryExpr) exprNode()      {}
func (*SubqueryExpr) String() string { return "(SELECT ...)" }

// Statement is a parsed SQL statement.
type Statement interface {
	stmtNode()
	String() string
}

// SetClause is one assignment in an UPDATE SET list.
type SetClause struct {
	Column string
	Value  Expr
}

// UpdateStmt is a parsed UPDATE.
type UpdateStmt struct {
	Table string
	Set   []SetClause
	Where Expr
}

func (*UpdateStmt) stmtNode() {}

func (s *UpdateStmt) String() string {
	sets := make([]string, len(s.Set))
	for i, c := range s.Set {
		sets[i] = c.Column + " = " + c.Value.String()
	}
	out := "UPDATE " + s.Table + " SET " + strings.Join(sets, ", ")
	if s.Where != nil {
		out += " WHERE " + s.Where.String()
	}
	return out
}

// DeleteStmt is a parsed DELETE.
type DeleteStmt struct {
	Table string
	Where Expr
}

func (*DeleteStmt) stmtNode() {}

func (s *DeleteStmt) String() string {
	out := "DELETE FROM " + s.Table
	if s.Where != nil {
		out += " WHERE " + s.Where.String()
	}
	return out
}

// InsertStmt is a parsed INSERT with literal rows.
type InsertStmt struct {
	Table   string
	Columns []string
	Rows    [][]Expr
}

func (*InsertStmt) stmtNode() {}

func (s *InsertStmt) String() string {
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES %d row(s)",
		s.Table, strings.Join(s.Columns, ", "), len(s.Rows))
}

// SelectStmt is a parsed SELECT. Only used to represent (rejected)
// subqueries; the enforcer does not accept it as a write statement.
type SelectStmt struct {
	Table string
	Where Expr
}

func (*SelectStmt) stmtNode()      {}
func (*SelectStmt) String() string { return "SELECT ..." }

// walk visits e and every descendant expression in depth-first order.
func walk(e Expr, fn func(Expr) error) error {
	if e == nil {
		return nil
	}
	if err := fn(e); err != nil {
		return err
	}
	switch n := e.(type) {
	case *BinaryExpr:
		if err := walk(n.Left, fn); err != nil {
			return err
		}
		return walk(n.Right, fn)
	case *UnaryExpr:
		return walk(n.X, fn)
	case *ParenExpr:
		return walk(n.X, fn)
	case *FuncCall:
		for _, a := range n.Args {
			if err := walk(a, fn); err != nil {
				return err
			}
		}
	}
	return nil
}
