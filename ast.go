package netsec

import "math/big"

// Expr is a side-effect-free boolean expression. Variables hold boolean
// values; an unset variable is an execution error rather than false.
type Expr interface{ expr() }

// Var reads a named boolean variable.
type Var string

// Lit is a boolean literal.
type Lit bool

// Not negates its operand.
type Not struct{ X Expr }

// And is conjunction over total boolean expressions.
type And struct{ X, Y Expr }

// Or is disjunction.
type Or struct{ X, Y Expr }

func (Var) expr() {}
func (Lit) expr() {}
func (Not) expr() {}
func (And) expr() {}
func (Or) expr()  {}

// Stmt is one program statement.
type Stmt interface{ stmt() }

// Assign binds a boolean variable.
type Assign struct {
	Name string
	Expr Expr
}

// Flip is a probabilistic branch. Each branch carries an exact rational
// weight. A node whose weights are negative or whose weight sum is not
// exactly one is rejected.
type Flip struct {
	NodeID   string
	Branches []Branch
}

// Branch is one weighted alternative of a Flip node.
type Branch struct {
	Weight *big.Rat
	Body   []Stmt
}

// While is a bounded loop: at most Limit iterations of Body run while Cond
// stays true. Exceeding Limit raises a LimitError.
type While struct {
	Cond  Expr
	Limit int
	Body  []Stmt
}

// Repeat runs Body exactly Count times.
type Repeat struct {
	Count int
	Body  []Stmt
}

// Observe appends an observable (Label, value) pair to the output trace.
type Observe struct {
	Label string
	Expr  Expr
}

// Fail raises a named exception, terminating the run immediately. It models
// early anomalies such as authentication failure inside nested loops.
type Fail struct{ Name string }

// Seq is an explicitly grouped statement block.
type Seq struct{ Body []Stmt }

func (Assign) stmt()  {}
func (Flip) stmt()    {}
func (While) stmt()   {}
func (Repeat) stmt()  {}
func (Observe) stmt() {}
func (Fail) stmt()    {}
func (Seq) stmt()     {}
