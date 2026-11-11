package netsec

// MaxVars is the maximum number of boolean configuration variables
// supported by platform conditions and mutex groups.
const MaxVars = 16

// Cond is a boolean condition over build configuration variables.
// Variables are indexed 0..MaxVars-1 and an assignment is a bitset
// in which bit i holds the value of variable i.
type Cond interface {
	Eval(assign uint32) bool
}

type constCond bool

func (c constCond) Eval(uint32) bool { return bool(c) }

// Always returns a condition that holds under every assignment.
func Always() Cond { return constCond(true) }

// Never returns a condition that holds under no assignment.
func Never() Cond { return constCond(false) }

type varCond int

func (v varCond) Eval(assign uint32) bool { return assign&(1<<uint(v)) != 0 }

// Var returns a condition that holds when variable i is true.
// i must be in [0, MaxVars).
func Var(i int) Cond { return varCond(i) }

type notCond struct{ c Cond }

func (n notCond) Eval(assign uint32) bool { return !n.c.Eval(assign) }

// Not returns the negation of c.
func Not(c Cond) Cond { return notCond{c} }

type andCond []Cond

func (a andCond) Eval(assign uint32) bool {
	for _, c := range a {
		if !c.Eval(assign) {
			return false
		}
	}
	return true
}

// And returns a condition that holds when all of cs hold.
func And(cs ...Cond) Cond { return andCond(cs) }

type orCond []Cond

func (o orCond) Eval(assign uint32) bool {
	for _, c := range o {
		if c.Eval(assign) {
			return true
		}
	}
	return false
}

// Or returns a condition that holds when at least one of cs holds.
func Or(cs ...Cond) Cond { return orCond(cs) }
