// Package checker implements an exhaustive loop-invariant checker over a
// finite integer domain. Programs consist of affine parallel assignments and
// linear guards; the candidate invariant and the postcondition are supplied
// as input. The checker verifies initiation, one-step preservation and
// sufficiency for the postcondition by enumerating every state in the
// declared finite bounds.
package checker

import (
	"fmt"
	"math/big"
	"sort"
)

// LinearExpr is an affine expression sum(coeffs[v]*v) + const.
type LinearExpr struct {
	Coeffs map[string]int64 `json:"coeffs"`
	Const  int64            `json:"const"`
}

// Eval evaluates the expression at a state using big integers so that
// intermediate results cannot overflow.
func (e LinearExpr) Eval(s State) *big.Int {
	acc := big.NewInt(e.Const)
	for v, c := range e.Coeffs {
		term := big.NewInt(c)
		term.Mul(term, big.NewInt(s[v]))
		acc.Add(acc, term)
	}
	return acc
}

// Constraint is a linear comparison expr <op> 0.
type Constraint struct {
	Expr LinearExpr `json:"expr"`
	Op   string     `json:"op"`
}

var validOps = map[string]bool{">=": true, "<=": true, ">": true, "<": true, "==": true, "!=": true}

// Holds reports whether the constraint is satisfied at state s.
func (c Constraint) Holds(s State) bool {
	sign := c.Expr.Eval(s).Sign()
	switch c.Op {
	case ">=":
		return sign >= 0
	case "<=":
		return sign <= 0
	case ">":
		return sign > 0
	case "<":
		return sign < 0
	case "==":
		return sign == 0
	case "!=":
		return sign != 0
	}
	return false
}

// Assignment is one parallel assignment target := affine(old state).
type Assignment struct {
	Var  string     `json:"var"`
	Expr LinearExpr `json:"expr"`
}

// State maps variable names to values inside the finite domain.
type State map[string]int64

// Program describes the loop, the candidate invariant and the postcondition.
type Program struct {
	Variables []string            `json:"variables"`
	Bounds    map[string][2]int64 `json:"bounds"`
	Init      []Constraint        `json:"init"`
	Guard     []Constraint        `json:"guard"`
	Body      []Assignment        `json:"body"`
	Invariant []Constraint        `json:"invariant"`
	Post      []Constraint        `json:"post"`
}

// Transition records one concrete step of the loop body.
type Transition struct {
	From State             `json:"from"`
	To   map[string]string `json:"to"`
}

// InitResult reports whether init implies the invariant.
type InitResult struct {
	Holds          bool   `json:"holds"`
	Counterexample *State `json:"counterexample,omitempty"`
}

// PreservationResult reports whether invariant ∧ guard is preserved by the
// body. Successors that fall outside the declared bounds are listed
// separately and never counted as evidence that preservation holds.
type PreservationResult struct {
	Holds          bool         `json:"holds"`
	Counterexample *Transition  `json:"counterexample,omitempty"`
	OutOfBounds    []Transition `json:"outOfBounds,omitempty"`
}

// SufficiencyResult reports whether invariant ∧ ¬guard implies the
// postcondition.
type SufficiencyResult struct {
	Holds          bool   `json:"holds"`
	Counterexample *State `json:"counterexample,omitempty"`
}

// Report is the full outcome of a check.
type Report struct {
	Verdict      string             `json:"verdict"` // "valid", "invalid" or "unknown"
	Init         InitResult         `json:"init"`
	Preservation PreservationResult `json:"preservation"`
	Sufficiency  SufficiencyResult  `json:"sufficiency"`
}

const (
	VerdictValid   = "valid"
	VerdictInvalid = "invalid"
	VerdictUnknown = "unknown"
)

// Validate checks structural well-formedness and that the enumeration space
// does not exceed maxStates.
func (p *Program) Validate(maxStates uint64) error {
	if len(p.Variables) == 0 {
		return fmt.Errorf("no variables declared")
	}
	seen := make(map[string]bool)
	for _, v := range p.Variables {
		if seen[v] {
			return fmt.Errorf("duplicate variable %q", v)
		}
		seen[v] = true
		b, ok := p.Bounds[v]
		if !ok {
			return fmt.Errorf("missing bounds for variable %q", v)
		}
		if b[0] > b[1] {
			return fmt.Errorf("empty bounds for variable %q: [%d, %d]", v, b[0], b[1])
		}
	}
	checkExpr := func(e LinearExpr, where string) error {
		for v := range e.Coeffs {
			if !seen[v] {
				return fmt.Errorf("%s: unknown variable %q", where, v)
			}
		}
		return nil
	}
	checkConstraints := func(cs []Constraint, where string) error {
		for i, c := range cs {
			if !validOps[c.Op] {
				return fmt.Errorf("%s[%d]: invalid operator %q", where, i, c.Op)
			}
			if err := checkExpr(c.Expr, fmt.Sprintf("%s[%d]", where, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := checkConstraints(p.Init, "init"); err != nil {
		return err
	}
	if err := checkConstraints(p.Guard, "guard"); err != nil {
		return err
	}
	if err := checkConstraints(p.Invariant, "invariant"); err != nil {
		return err
	}
	if err := checkConstraints(p.Post, "post"); err != nil {
		return err
	}
	assigned := make(map[string]bool)
	for i, a := range p.Body {
		if !seen[a.Var] {
			return fmt.Errorf("body[%d]: unknown assignment target %q", i, a.Var)
		}
		if assigned[a.Var] {
			return fmt.Errorf("body[%d]: variable %q assigned twice", i, a.Var)
		}
		assigned[a.Var] = true
		if err := checkExpr(a.Expr, fmt.Sprintf("body[%d]", i)); err != nil {
			return err
		}
	}
	var space uint64 = 1
	for _, v := range p.Variables {
		b := p.Bounds[v]
		n := uint64(b[1]-b[0]) + 1
		if space > (maxStates+n-1)/n {
			return fmt.Errorf("state space exceeds limit of %d states", maxStates)
		}
		space *= n
		if space > maxStates {
			return fmt.Errorf("state space exceeds limit of %d states", maxStates)
		}
	}
	return nil
}

func allHold(cs []Constraint, s State) bool {
	for _, c := range cs {
		if !c.Holds(s) {
			return false
		}
	}
	return true
}

// enumerate visits every state within the declared bounds until fn returns
// false.
func (p *Program) enumerate(fn func(State) bool) {
	vars := append([]string(nil), p.Variables...)
	sort.Strings(vars)
	state := make(State, len(vars))
	var rec func(i int) bool
	rec = func(i int) bool {
		if i == len(vars) {
			return fn(state)
		}
		b := p.Bounds[vars[i]]
		for x := b[0]; x <= b[1]; x++ {
			state[vars[i]] = x
			if !rec(i + 1) {
				return false
			}
		}
		return true
	}
	rec(0)
}

// step applies the parallel body to s. It returns the successor state and
// whether every assigned value stays inside the declared bounds.
func (p *Program) step(s State) (State, bool) {
	next := make(State, len(p.Variables))
	for _, v := range p.Variables {
		next[v] = s[v]
	}
	inBounds := true
	for _, a := range p.Body {
		val := a.Expr.Eval(s)
		b := p.Bounds[a.Var]
		if !val.IsInt64() || val.Int64() < b[0] || val.Int64() > b[1] {
			inBounds = false
			continue
		}
		next[a.Var] = val.Int64()
	}
	return next, inBounds
}

func copyState(s State) State {
	c := make(State, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}

func stringState(s State) map[string]string {
	m := make(map[string]string, len(s))
	for k, v := range s {
		m[k] = fmt.Sprintf("%d", v)
	}
	return m
}

// rawSuccessor computes the successor as arbitrary-precision values,
// regardless of the declared bounds (used to report out-of-bounds steps).
func (p *Program) rawSuccessor(s State) map[string]string {
	next := stringState(s)
	for _, a := range p.Body {
		next[a.Var] = a.Expr.Eval(s).String()
	}
	return next
}

// Check exhaustively verifies initiation, preservation and sufficiency.
func (p *Program) Check() *Report {
	rep := &Report{}
	rep.Init.Holds = true
	rep.Preservation.Holds = true
	rep.Sufficiency.Holds = true

	// Initiation: init ==> invariant.
	p.enumerate(func(s State) bool {
		if allHold(p.Init, s) && !allHold(p.Invariant, s) {
			bad := copyState(s)
			rep.Init.Holds = false
			rep.Init.Counterexample = &bad
			return false
		}
		return true
	})

	// Preservation: invariant ∧ guard ==> invariant(body(s)).
	p.enumerate(func(s State) bool {
		if !allHold(p.Invariant, s) || !allHold(p.Guard, s) {
			return true
		}
		next, inBounds := p.step(s)
		if !inBounds {
			rep.Preservation.OutOfBounds = append(rep.Preservation.OutOfBounds, Transition{
				From: copyState(s),
				To:   p.rawSuccessor(s),
			})
			return true
		}
		if !allHold(p.Invariant, next) {
			rep.Preservation.Holds = false
			rep.Preservation.Counterexample = &Transition{
				From: copyState(s),
				To:   stringState(next),
			}
			return false
		}
		return true
	})

	// Sufficiency: invariant ∧ ¬guard ==> post.
	p.enumerate(func(s State) bool {
		if allHold(p.Invariant, s) && !allHold(p.Guard, s) && !allHold(p.Post, s) {
			bad := copyState(s)
			rep.Sufficiency.Holds = false
			rep.Sufficiency.Counterexample = &bad
			return false
		}
		return true
	})

	switch {
	case !rep.Init.Holds || !rep.Preservation.Holds || !rep.Sufficiency.Holds:
		rep.Verdict = VerdictInvalid
	case len(rep.Preservation.OutOfBounds) > 0:
		// Out-of-bounds steps could not be verified, so the invariant is not
		// proven; report them separately instead of claiming validity.
		rep.Verdict = VerdictUnknown
	default:
		rep.Verdict = VerdictValid
	}
	return rep
}
