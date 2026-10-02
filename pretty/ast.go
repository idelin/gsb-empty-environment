// Package pretty implements a bidirectional parser and a pretty printer
// for a small expression language. The printer reconstructs formatted
// source code from an AST while respecting indentation levels, a line
// width limit and line-breaking rules; re-parsing the printed output
// yields an AST structurally identical to the original.
package pretty

// Expr is the interface implemented by all AST nodes.
type Expr interface{ exprNode() }

// Number is a numeric literal. Value keeps the exact source text.
type Number struct{ Value string }

// String is a string literal (unquoted value).
type String struct{ Value string }

// Ident is an identifier.
type Ident struct{ Name string }

// Unary is a prefix operation such as -x or !flag.
type Unary struct {
	Op string
	X  Expr
}

// Binary is an infix operation such as a + b.
type Binary struct {
	Op   string
	X, Y Expr
}

// Call is a function invocation: Func(arg1, arg2, ...).
type Call struct {
	Func Expr
	Args []Expr
}

func (*Number) exprNode() {}
func (*String) exprNode() {}
func (*Ident) exprNode()  {}
func (*Unary) exprNode()  {}
func (*Binary) exprNode() {}
func (*Call) exprNode()   {}

// Precedence levels. Larger binds tighter.
const (
	lowestPrec = 0
	unaryPrec  = 7
	atomPrec   = 8
)

// binPrec maps a binary operator to its precedence level.
func binPrec(op string) int {
	switch op {
	case "||":
		return 1
	case "&&":
		return 2
	case "==", "!=":
		return 3
	case "<", "<=", ">", ">=":
		return 4
	case "+", "-":
		return 5
	case "*", "/", "%":
		return 6
	}
	return -1
}

// exprPrec reports the precedence of an expression's root node.
func exprPrec(e Expr) int {
	switch n := e.(type) {
	case *Binary:
		return binPrec(n.Op)
	case *Unary:
		return unaryPrec
	default:
		return atomPrec
	}
}
