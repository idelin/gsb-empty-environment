package netsec

// MaxConditions is the maximum number of boolean conditions a plan may
// reference. The enumeration interpreter evaluates every assignment, so the
// search space is 2^MaxConditions.
const MaxConditions = 12

// Assignment is a concrete truth assignment to the boolean conditions of an
// install plan. Bit i holds the value of condition Var(i).
type Assignment uint16

// Bit reports the value of condition i.
func (a Assignment) Bit(i int) bool { return a&(1<<uint(i)) != 0 }

// Cond is a boolean condition over plan variables.
type Cond interface {
	// Eval evaluates the condition under a concrete assignment.
	Eval(a Assignment) bool
	// Vars returns the bitmask of referenced variable indices.
	Vars() int
}

// Var references the i-th boolean condition.
type Var int

func (v Var) Eval(a Assignment) bool { return a.Bit(int(v)) }
func (v Var) Vars() int              { return 1 << uint(v) }

// Const is a constant condition.
type Const bool

func (c Const) Eval(Assignment) bool { return bool(c) }
func (c Const) Vars() int            { return 0 }

// Not negates a condition.
type Not struct{ C Cond }

func (n Not) Eval(a Assignment) bool { return !n.C.Eval(a) }
func (n Not) Vars() int              { return n.C.Vars() }

// And conjoins two conditions.
type And struct{ L, R Cond }

func (c And) Eval(a Assignment) bool { return c.L.Eval(a) && c.R.Eval(a) }
func (c And) Vars() int              { return c.L.Vars() | c.R.Vars() }

// Or disjoins two conditions.
type Or struct{ L, R Cond }

func (c Or) Eval(a Assignment) bool { return c.L.Eval(a) || c.R.Eval(a) }
func (c Or) Vars() int              { return c.L.Vars() | c.R.Vars() }

// Node is an install-plan operation.
type Node interface{ planNode() }

// Seq executes nodes in order.
type Seq struct{ Nodes []Node }

// Read reads a path.
type Read struct{ Path string }

// Write writes a path.
type Write struct{ Path string }

// Call invokes a trusted function by name.
type Call struct{ Func string }

// If executes Then when Cond holds, Else otherwise.
type If struct {
	Cond Cond
	Then Node
	Else Node
}

// Loop executes Body exactly Count times.
type Loop struct {
	Count int
	Body  Node
}

func (Seq) planNode()   {}
func (Read) planNode()  {}
func (Write) planNode() {}
func (Call) planNode()  {}
func (If) planNode()    {}
func (Loop) planNode()  {}
