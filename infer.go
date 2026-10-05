package unitinfer

import "fmt"

// Constraint is a unit equation Term = 0 together with its provenance: the
// expression node whose typing rule generated it.
type Constraint struct {
	Term Term
	Node int    // expression node id
	Desc string // human-readable origin
}

func (c Constraint) String() string {
	return fmt.Sprintf("node#%d: %s = 0   (%s)", c.Node, c.Term.String(nil, defaultVarName), c.Desc)
}

func defaultVarName(v int) string { return fmt.Sprintf("u%d", v) }

// Inferencer walks an expression and emits unit constraints.
type Inferencer struct {
	dims     int
	nextVar  int
	vars     map[string]int // program variable name -> unit variable
	nodeUnit map[int]int    // expression node id -> unit variable
	hints    map[int]string // unit variable -> display hint
	cons     []Constraint
	order    []int // unit variable creation order
}

// NewInferencer creates an Inferencer over dims base quantities.
func NewInferencer(dims int) *Inferencer {
	return &Inferencer{
		dims:     dims,
		vars:     map[string]int{},
		nodeUnit: map[int]int{},
		hints:    map[int]string{},
	}
}

func (in *Inferencer) fresh(hint string) int {
	v := in.nextVar
	in.nextVar++
	in.hints[v] = hint
	in.order = append(in.order, v)
	return v
}

// VarName returns a display name for a unit variable.
func (in *Inferencer) VarName(v int) string {
	if h, ok := in.hints[v]; ok && h != "" {
		return fmt.Sprintf("u%d<%s>", v, h)
	}
	return defaultVarName(v)
}

// Constraints returns the generated constraints.
func (in *Inferencer) Constraints() []Constraint { return in.cons }

// Vars returns all unit variables ever created, in creation order.
func (in *Inferencer) Vars() []int {
	out := make([]int, len(in.order))
	copy(out, in.order)
	return out
}

// UnitVar returns the unit variable assigned to expression node e.
func (in *Inferencer) UnitVar(e *Expr) int { return in.nodeUnit[e.ID()] }

// ConstrainVar fixes the unit of an already-inferred expression e to the
// constant term u (e.g. declaring "a is a length").
func (in *Inferencer) ConstrainVar(e *Expr, u Term, desc string) {
	in.eq(in.vt(in.nodeUnit[e.ID()]), u, e.ID(), desc)
}

func (in *Inferencer) constrain(t Term, node int, desc string) {
	in.cons = append(in.cons, Constraint{Term: t, Node: node, Desc: desc})
}

// eq constrains a == b.
func (in *Inferencer) eq(a, b Term, node int, desc string) {
	in.constrain(a.Sub(b), node, desc)
}

func (in *Inferencer) vt(v int) Term { return VarTerm(in.dims, v) }

// Infer generates constraints for e and returns the unit variable
// representing the unit of e's value.
func (in *Inferencer) Infer(e *Expr) int {
	if v, ok := in.nodeUnit[e.ID()]; ok {
		return v
	}
	var v int
	switch e.Kind {
	case KindNum:
		v = in.fresh("lit")
		in.eq(in.vt(v), e.Lit, e.ID(), "literal unit")
	case KindVar:
		if uv, ok := in.vars[e.Name]; ok {
			v = uv
		} else {
			v = in.fresh(e.Name)
			in.vars[e.Name] = v
		}
	case KindBin:
		l := in.Infer(e.L)
		r := in.Infer(e.R)
		v = in.fresh(e.Op.String())
		switch e.Op {
		case OpAdd, OpSub:
			in.eq(in.vt(l), in.vt(r), e.ID(),
				fmt.Sprintf("operands of %q must have equal units", e.Op))
			in.eq(in.vt(v), in.vt(l), e.ID(),
				fmt.Sprintf("result of %q takes the operand unit", e.Op))
		case OpMul:
			in.eq(in.vt(v), in.vt(l).Add(in.vt(r)), e.ID(),
				"result of * adds exponents")
		case OpDiv:
			in.eq(in.vt(v), in.vt(l).Sub(in.vt(r)), e.ID(),
				"result of / subtracts exponents")
		}
	case KindPow:
		x := in.Infer(e.Base)
		v = in.fresh("pow")
		in.eq(in.vt(v), in.vt(x).Scale(e.Exp), e.ID(),
			fmt.Sprintf("integer power multiplies exponents by %d", e.Exp))
	case KindCall:
		mapping := map[int]int{}
		for i := 0; i < e.Sig.NVars; i++ {
			mapping[i] = in.fresh(fmt.Sprintf("%s.a%d", e.Sig.Name, i))
		}
		for i, arg := range e.Args {
			av := in.Infer(arg)
			pt := e.Sig.Params[i].Subst(mapping)
			in.eq(in.vt(av), pt, e.ID(),
				fmt.Sprintf("argument %d of %s", i, e.Sig.Name))
		}
		v = in.fresh(e.Sig.Name + ".ret")
		rt := e.Sig.Result.Subst(mapping)
		in.eq(in.vt(v), rt, e.ID(), "result of "+e.Sig.Name)
	}
	in.nodeUnit[e.ID()] = v
	return v
}
