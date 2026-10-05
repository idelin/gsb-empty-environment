package main

// Expr is a boolean or integer expression.
type Expr interface{ exprNode() }

// Const is a signed 8-bit integer literal.
type Const struct {
	Val  int8
	Line int
}

// Var references an input variable or an assigned variable.
type Var struct {
	Name string
	Line int
}

// Neg is arithmetic negation (-x), wrapping in signed 8-bit.
type Neg struct {
	E    Expr
	Line int
}

// Bin is an arithmetic operator: "+" or "-".
type Bin struct {
	Op   string
	L, R Expr
	Line int
}

// Cmp is a comparison operator producing a boolean:
// "==", "!=", "<", "<=", ">", ">=".
type Cmp struct {
	Op   string
	L, R Expr
	Line int
}

// Not is boolean negation. Only produced by mutation.
type Not struct {
	E    Expr
	Line int
}

func (*Const) exprNode() {}
func (*Var) exprNode()   {}
func (*Neg) exprNode()   {}
func (*Bin) exprNode()   {}
func (*Cmp) exprNode()   {}
func (*Not) exprNode()   {}

// Stmt is a statement.
type Stmt interface{ stmtNode() }

// Assign binds a variable to an expression value.
type Assign struct {
	Name string
	E    Expr
	Line int
}

// If is a conditional branch; Cond must evaluate to a boolean.
type If struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
	Line int
}

// Return stops execution and yields the program output.
type Return struct {
	E    Expr
	Line int
}

func (*Assign) stmtNode() {}
func (*If) stmtNode()     {}
func (*Return) stmtNode() {}

// Program is a full program: named inputs plus a statement list.
type Program struct {
	Inputs []string
	Body   []Stmt
}
