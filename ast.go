// Package exprfmt implements a small expression language with a parser and
// a pretty-printer. The printer renders an AST back to source code, keeping
// lines within 80 columns by breaking at operators and commas, and the
// printed output parses back to an identical AST.
package exprfmt

// Expr is the interface implemented by all AST nodes.
type Expr interface{ exprNode() }

// Number is a numeric literal. Value holds the source text.
type Number struct{ Value string }

// Str is a string literal. Value holds the quoted source text.
type Str struct{ Value string }

// Ident is an identifier.
type Ident struct{ Name string }

// Unary is a prefix operation, e.g. -x or !x.
type Unary struct {
	Op string
	X  Expr
}

// Binary is an infix operation, e.g. x + y.
type Binary struct {
	Op   string
	X, Y Expr
}

// Call is a function call, e.g. f(a, b).
type Call struct {
	Func Expr
	Args []Expr
}

func (*Number) exprNode() {}
func (*Str) exprNode()    {}
func (*Ident) exprNode()  {}
func (*Unary) exprNode()  {}
func (*Binary) exprNode() {}
func (*Call) exprNode()   {}
