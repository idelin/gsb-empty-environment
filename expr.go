package unitinfer

import "fmt"

// Expr is a scalar expression whose unit is to be inferred.
type Expr interface{ exprNode() }

// Var is a source variable with an (initially unknown) unit.
type Var struct{ Name string }

// Const is a literal with a known concrete unit (nil/zero = dimensionless).
type Const struct{ Unit []int }

// Add/Sub require both sides to have the same unit.
type Add struct{ L, R Expr }
type Sub struct{ L, R Expr }

// Mul/Div add/subtract the exponent vectors.
type Mul struct{ L, R Expr }
type Div struct{ L, R Expr }

// Pow raises to an integer power (negative allowed), scaling exponents.
type Pow struct {
	Base Expr
	N    int
}

// Call applies a unit-polymorphic function.
type Call struct {
	Fn   *Signature
	Args []Expr
}

func (Var) exprNode()   {}
func (Const) exprNode() {}
func (Add) exprNode()   {}
func (Sub) exprNode()   {}
func (Mul) exprNode()   {}
func (Div) exprNode()   {}
func (Pow) exprNode()   {}
func (Call) exprNode()  {}

// Signature is a unit-polymorphic function type. Params and Result are Terms
// over signature-local unit variables 0..NumVars-1; each call site gets fresh
// instantiations. Example: square = forall a. a -> a^2 has NumVars=1,
// Params=[X0], Result=[2*X0].
type Signature struct {
	Name    string
	NumVars int
	Params  []Term
	Result  Term
}

// Constraint records that two unit terms must be equal, and where the
// requirement came from (used to report minimal conflict sets).
type Constraint struct {
	LHS, RHS Term
	Where    string
}

// Infer accumulates unit variables and equality constraints for a program.
type Infer struct {
	NumDims     int
	DimNames    []string
	constraints []Constraint
	varNames    []string
	env         map[string]int
	callSites   int
}

// NewInfer creates an inference context over numDims base dimensions.
func NewInfer(numDims int, dimNames []string) *Infer {
	return &Infer{NumDims: numDims, DimNames: dimNames, env: map[string]int{}}
}

// FreshVar allocates a new unit variable.
func (in *Infer) FreshVar(name string) int {
	id := len(in.varNames)
	in.varNames = append(in.varNames, name)
	return id
}

// NumVars returns the number of unit variables allocated so far.
func (in *Infer) NumVars() int { return len(in.varNames) }

// Constraints returns the accumulated constraints.
func (in *Infer) Constraints() []Constraint { return in.constraints }

// ConstrainEq requires the two terms to denote the same unit.
func (in *Infer) ConstrainEq(a, b Term, where string) {
	in.constraints = append(in.constraints, Constraint{LHS: a, RHS: b, Where: where})
}

// Gen infers the unit Term of e, emitting constraints as a side effect.
// Repeated references to the same source Var share one unit variable.
func (in *Infer) Gen(e Expr) Term {
	switch ex := e.(type) {
	case Var:
		id, ok := in.env[ex.Name]
		if !ok {
			id = in.FreshVar(ex.Name)
			in.env[ex.Name] = id
		}
		return VarTerm(id)
	case Const:
		return ConstTerm(ex.Unit)
	case Add:
		return in.genAdditive("add", ex.L, ex.R)
	case Sub:
		return in.genAdditive("sub", ex.L, ex.R)
	case Mul:
		return in.Gen(ex.L).Add(in.Gen(ex.R))
	case Div:
		return in.Gen(ex.L).Sub(in.Gen(ex.R))
	case Pow:
		return in.Gen(ex.Base).Scale(ex.N)
	case Call:
		return in.genCall(ex)
	default:
		panic(fmt.Sprintf("unitinfer: unknown expression %T", e))
	}
}

func (in *Infer) genAdditive(op string, l, r Expr) Term {
	tl := in.Gen(l)
	tr := in.Gen(r)
	in.ConstrainEq(tl, tr, fmt.Sprintf("%s operands must share a unit", op))
	return tl
}

func (in *Infer) genCall(c Call) Term {
	if len(c.Args) != len(c.Fn.Params) {
		panic(fmt.Sprintf("unitinfer: %s expects %d args, got %d", c.Fn.Name, len(c.Fn.Params), len(c.Args)))
	}
	in.callSites++
	site := in.callSites
	local := make([]int, c.Fn.NumVars)
	for i := range local {
		local[i] = in.FreshVar(fmt.Sprintf("%s#%d.a%d", c.Fn.Name, site, i))
	}
	instantiate := func(t Term) Term {
		co := map[int]int{}
		for v, k := range t.Coeffs {
			co[local[v]] += k
		}
		return Term{Const: t.Const, Coeffs: co}
	}
	for i, arg := range c.Args {
		ta := in.Gen(arg)
		in.ConstrainEq(instantiate(c.Fn.Params[i]), ta,
			fmt.Sprintf("arg %d of %s (call site %d)", i, c.Fn.Name, site))
	}
	return instantiate(c.Fn.Result)
}
