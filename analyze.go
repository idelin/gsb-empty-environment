package netsec

import "fmt"

// Options tunes the analysis and the concrete interpreter.
type Options struct {
	WidenDelay  int // loop-head joins before widening kicks in
	NarrowIters int // maximum descending (narrowing) sweeps
	InterpSteps int // total concrete states the interpreter may explore
	InterpPaths int // maximum completed concrete paths
}

// DefaultOptions returns a reasonable default configuration.
func DefaultOptions() Options {
	return Options{WidenDelay: 2, NarrowIters: 8, InterpSteps: 1 << 20, InterpPaths: 1 << 16}
}

// Constraint is a derived difference constraint X - Y <= Bound used as
// proof justification. Bound == Inf means "no bound".
type Constraint struct {
	X, Y  string
	Bound int64
}

func (c Constraint) String() string {
	if c.Bound == Inf {
		return fmt.Sprintf("%s - %s <= +inf", c.X, c.Y)
	}
	return fmt.Sprintf("%s - %s <= %d", c.X, c.Y, c.Bound)
}

// ConcreteResult summarises the bounded concrete interpreter's findings
// for one access.
type ConcreteResult struct {
	Reached    bool  // the access was executed at least once
	MinIndex   int64 // smallest observed concrete index (valid if Reached)
	MaxIndex   int64 // largest observed concrete index
	Violations int64 // concrete out-of-bounds executions
}

// AccessResult is the verdict for one array access.
type AccessResult struct {
	Access      Access
	Proven      bool // in-bounds for all executions (vacuously true if Unreachable)
	Unreachable bool // the access node is statically infeasible
	LowerOK     bool // derived 0 - idx <= Offset
	UpperOK     bool // derived idx - 0 <= len-1-Offset
	NeedLower   Constraint
	HaveLower   Constraint
	NeedUpper   Constraint
	HaveUpper   Constraint
	Basis       []Constraint // chain of difference constraints implying the proof
	Concrete    ConcreteResult
}

// Result is the output of Analyze.
type Result struct {
	Accesses        []AccessResult
	InterpTruncated bool // concrete interpreter hit a step/path budget

	g   *Graph
	inv []*dbm // final invariant per node (nil = unreachable)
}

// Bound returns the derived bound x - y <= c at a node. The boolean is
// false when the node is unreachable or a variable is unknown.
func (r *Result) Bound(node int, x, y string) (int64, bool) {
	if node < 0 || node >= len(r.inv) || r.inv[node] == nil {
		return 0, false
	}
	xi, ok1 := r.g.varIdx[x]
	yi, ok2 := r.g.varIdx[y]
	if !ok1 || !ok2 {
		return 0, false
	}
	return r.inv[node].at(xi, yi), true
}

// Analyze runs the difference-constraint analysis over g: closed-DBM
// propagation with widening at loop heads followed by narrowing, then
// per-access bound proofs, then a bounded concrete interpretation that
// double-checks every proven access.
func Analyze(g *Graph, opt Options) *Result {
	if opt == (Options{}) {
		opt = DefaultOptions()
	}
	heads := loopHeads(g)
	pre := ascend(g, heads, opt)
	pre = descend(g, heads, pre, opt)

	res := &Result{g: g, inv: pre}
	conc := g.interpret(opt)
	res.InterpTruncated = conc.truncated
	for _, a := range g.accesses {
		ar := g.checkAccess(pre[a.Node], a)
		ar.Concrete = conc.perAccess[a.ID]
		res.Accesses = append(res.Accesses, ar)
	}
	return res
}
