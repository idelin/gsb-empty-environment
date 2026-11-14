// Package scim provides an importable authorization component for SCIM
// (RFC 7644) filtered queries against a multi-tenant user directory.
//
// Filters are parsed into a syntax tree and validated node by node
// (operator, attribute, value) against a Policy before any evaluation
// happens. Evaluation is always performed inside the caller's tenant
// scope, so OR / NOT constructs can never widen the result set beyond
// the tenant boundary.
package scim

// Op is a SCIM comparison operator.
type Op string

const (
	OpEq Op = "eq"
	OpNe Op = "ne"
	OpCo Op = "co"
	OpSw Op = "sw"
	OpEw Op = "ew"
	OpGt Op = "gt"
	OpLt Op = "lt"
	OpGe Op = "ge"
	OpLe Op = "le"
	OpPr Op = "pr"
)

// LogOp is a logical connective.
type LogOp string

const (
	LogAnd LogOp = "and"
	LogOr  LogOp = "or"
)

// Expr is a node of the parsed filter syntax tree.
type Expr interface{ exprNode() }

// LogExpr is an n-ary AND / OR combination.
type LogExpr struct {
	Op   LogOp
	Args []Expr
}

// NotExpr negates its sub-expression. Policy restricts where it may appear.
type NotExpr struct {
	Sub Expr
}

// AttrExpr compares an attribute with a literal value (or tests presence).
type AttrExpr struct {
	Path  string
	Op    Op
	Value Value
}

// ValuePath filters a multi-valued attribute, e.g. emails[value eq "a@b.c"].
type ValuePath struct {
	Path string
	Sub  Expr
}

func (*LogExpr) exprNode()   {}
func (*NotExpr) exprNode()   {}
func (*AttrExpr) exprNode()  {}
func (*ValuePath) exprNode() {}

// ValueKind classifies a comparison literal.
type ValueKind int

const (
	ValNull ValueKind = iota
	ValString
	ValNumber
	ValBool
)

// Value is a literal comparison value.
type Value struct {
	Kind ValueKind
	Str  string
	Num  float64
	Bool bool
}

// StringValue builds a string literal.
func StringValue(s string) Value { return Value{Kind: ValString, Str: s} }
