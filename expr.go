package relalg

import "strings"

// Expr 是 SQL 查询核心 AST 中的标量表达式节点。
type Expr interface {
	String() string
	exprNode()
}

// ColumnRef 表示列引用，如 u.name；Table 为表别名，可为空（未限定）。
type ColumnRef struct {
	Table string
	Name  string
}

func (e *ColumnRef) String() string {
	if e.Table == "" {
		return e.Name
	}
	return e.Table + "." + e.Name
}
func (e *ColumnRef) exprNode() {}

// LitKind 字面量类别。
type LitKind int

const (
	LitNumber LitKind = iota
	LitString
)

// Literal 表示数字或字符串字面量。
type Literal struct {
	Kind  LitKind
	Value string
}

func (e *Literal) String() string {
	if e.Kind == LitString {
		return "'" + strings.Replace(e.Value, "'", "''", -1) + "'"
	}
	return e.Value
}
func (e *Literal) exprNode() {}

// Star 表示 * 或 t.*。
type Star struct {
	Table string // 裸 * 时为空
}

func (e *Star) String() string {
	if e.Table == "" {
		return "*"
	}
	return e.Table + ".*"
}
func (e *Star) exprNode() {}

// BinaryExpr 表示二元表达式：比较运算（=, >, <, >=, <=, <>, !=）
// 或逻辑运算（AND, OR）。
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (e *BinaryExpr) String() string {
	l := operandString(e.Left, prec(e.Op))
	r := operandString(e.Right, prec(e.Op))
	return l + " " + e.Op + " " + r
}
func (e *BinaryExpr) exprNode() {}

// operandString 按优先级决定是否给子表达式加括号。
func operandString(e Expr, parentPrec int) string {
	s := e.String()
	if b, ok := e.(*BinaryExpr); ok && prec(b.Op) < parentPrec {
		return "(" + s + ")"
	}
	return s
}

func prec(op string) int {
	switch op {
	case "OR":
		return 1
	case "AND":
		return 2
	default:
		return 3
	}
}

// WalkExpr 先序遍历表达式树。
func WalkExpr(e Expr, visit func(Expr)) {
	if e == nil {
		return
	}
	visit(e)
	if b, ok := e.(*BinaryExpr); ok {
		WalkExpr(b.Left, visit)
		WalkExpr(b.Right, visit)
	}
}
