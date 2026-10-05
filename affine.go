// Package affarith implements affine arithmetic: every quantity is kept as
//
//	x = c0 + c1*e1 + c2*e2 + ... + cn*en,   ei in [-1, 1]
//
// where each noise symbol ei identifies a *specific* source of uncertainty.
// Because symbols are shared, correlated terms cancel exactly: x - x is 0,
// not an interval of double width. Nonlinear operations (multiplication,
// reciprocal) introduce a fresh noise symbol that conservatively encloses
// the quadratic / approximation remainder. When the number of symbols
// exceeds the engine's limit, the smallest coefficients are merged into a
// single conservative symbol.
//
// Every Affine records the expression DAG that produced it, so results can
// be exported as a Certificate and re-checked with exact rational
// arithmetic on a grid plus independent (naive) interval arithmetic.
package affarith

import (
	"fmt"
	"math"
	"sort"
)

// roundUp inflates a nonnegative magnitude slightly so that floating-point
// rounding in the affine operations never makes the enclosure unsound.
func roundUp(r float64) float64 {
	if r > 0 {
		return r * (1 + 1e-12)
	}
	return 0
}

// Op identifies the operation recorded in an expression Node.
type Op int

const (
	OpVar Op = iota
	OpConst
	OpAdd
	OpSub
	OpMul
	OpDiv
)

func (o Op) String() string {
	switch o {
	case OpVar:
		return "var"
	case OpConst:
		return "const"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpDiv:
		return "div"
	}
	return "?"
}

// Node is one node of the expression DAG recorded alongside an Affine.
// Shared subexpressions are shared *Node pointers, so a verifier evaluates
// the same structure the user built.
type Node struct {
	Op    Op
	Args  []*Node
	Var   int     // variable index, for OpVar
	Value float64 // constant value, for OpConst
}

// Engine allocates noise symbols and tracks variable domains. All Affine
// values combined in one expression must come from the same Engine.
// maxNoise limits the number of noise symbols per Affine (0 = unlimited);
// excess symbols are merged conservatively.
type Engine struct {
	maxNoise int
	nextSym  int
	vars     []Interval
}

// NewEngine creates an Engine. maxNoise <= 0 means no limit on the number
// of noise symbols per affine form.
func NewEngine(maxNoise int) *Engine {
	return &Engine{maxNoise: maxNoise}
}

func (e *Engine) fresh() int {
	s := e.nextSym
	e.nextSym++
	return s
}

// Var creates an input variable ranging over [lo, hi].
func (e *Engine) Var(lo, hi float64) Affine {
	if !(lo <= hi) {
		panic("affarith: empty variable domain")
	}
	id := len(e.vars)
	e.vars = append(e.vars, Interval{lo, hi})
	a := Affine{
		eng:    e,
		c0:     (lo + hi) / 2,
		coeffs: map[int]float64{},
		node:   &Node{Op: OpVar, Var: id},
	}
	if r := (hi - lo) / 2; r > 0 {
		a.coeffs[e.fresh()] = r
	}
	return a
}

// Const creates a constant affine form.
func (e *Engine) Const(c float64) Affine {
	return Affine{
		eng:    e,
		c0:     c,
		coeffs: map[int]float64{},
		node:   &Node{Op: OpConst, Value: c},
	}
}

// Affine is an affine form c0 + sum(ci * ei) with ei in [-1, 1].
type Affine struct {
	eng    *Engine
	c0     float64
	coeffs map[int]float64 // noise symbol id -> coefficient
	node   *Node
}

// Node returns the expression DAG node that produced this form.
func (a Affine) Node() *Node { return a.node }

// NumNoise returns the current number of noise symbols.
func (a Affine) NumNoise() int { return len(a.coeffs) }

// Interval converts the affine form to a conservative interval enclosure.
func (a Affine) Interval() Interval {
	r := 0.0
	for _, c := range a.coeffs {
		r += math.Abs(c)
	}
	r = roundUp(r)
	return Interval{a.c0 - r, a.c0 + r}
}

func (a Affine) mustShare(b Affine) {
	if a.eng != b.eng {
		panic("affarith: operands belong to different engines")
	}
}

// compact deletes exact-zero coefficients and, when the symbol count
// exceeds the engine limit, repeatedly merges the two smallest
// coefficients into one fresh symbol carrying the sum of their magnitudes
// (conservative, since |a*ei + b*ej| <= (|a|+|b|) * 1).
func (e *Engine) compact(c map[int]float64) {
	for id, v := range c {
		if v == 0 {
			delete(c, id)
		}
	}
	if e.maxNoise <= 0 {
		return
	}
	for len(c) > e.maxNoise {
		ids := make([]int, 0, len(c))
		for id := range c {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return math.Abs(c[ids[i]]) < math.Abs(c[ids[j]])
		})
		merged := roundUp(math.Abs(c[ids[0]]) + math.Abs(c[ids[1]]))
		delete(c, ids[0])
		delete(c, ids[1])
		c[e.fresh()] = merged
	}
}

// Add computes a + b. Shared noise symbols are added coefficient-wise, so
// dependencies are preserved.
func (a Affine) Add(b Affine) Affine {
	a.mustShare(b)
	r := Affine{
		eng:    a.eng,
		c0:     a.c0 + b.c0,
		coeffs: make(map[int]float64, len(a.coeffs)+len(b.coeffs)),
		node:   &Node{Op: OpAdd, Args: []*Node{a.node, b.node}},
	}
	for id, c := range a.coeffs {
		r.coeffs[id] += c
	}
	for id, c := range b.coeffs {
		r.coeffs[id] += c
	}
	r.eng.compact(r.coeffs)
	return r
}

// Neg computes -a.
func (a Affine) Neg() Affine {
	r := Affine{
		eng:    a.eng,
		c0:     -a.c0,
		coeffs: make(map[int]float64, len(a.coeffs)),
		node:   &Node{Op: OpSub, Args: []*Node{a.eng.Const(0).node, a.node}},
	}
	for id, c := range a.coeffs {
		r.coeffs[id] = -c
	}
	return r
}

// Sub computes a - b. Identical symbols cancel exactly: x.Sub(x) == 0.
func (a Affine) Sub(b Affine) Affine {
	a.mustShare(b)
	r := Affine{
		eng:    a.eng,
		c0:     a.c0 - b.c0,
		coeffs: make(map[int]float64, len(a.coeffs)+len(b.coeffs)),
		node:   &Node{Op: OpSub, Args: []*Node{a.node, b.node}},
	}
	for id, c := range a.coeffs {
		r.coeffs[id] += c
	}
	for id, c := range b.coeffs {
		r.coeffs[id] -= c
	}
	r.eng.compact(r.coeffs)
	return r
}

// Mul computes a * b. The product of the two deviation parts is enclosed
// conservatively by a fresh noise symbol whose coefficient is
// (sum|ai|) * (sum|bi|).
func (a Affine) Mul(b Affine) Affine {
	a.mustShare(b)
	r := Affine{
		eng:    a.eng,
		c0:     a.c0 * b.c0,
		coeffs: make(map[int]float64, len(a.coeffs)+len(b.coeffs)+1),
		node:   &Node{Op: OpMul, Args: []*Node{a.node, b.node}},
	}
	ra, rb := 0.0, 0.0
	for id, ca := range a.coeffs {
		ra += math.Abs(ca)
		r.coeffs[id] += ca * b.c0
	}
	for id, cb := range b.coeffs {
		rb += math.Abs(cb)
		r.coeffs[id] += a.c0 * cb
	}
	if rem := ra * rb; rem > 0 {
		r.coeffs[a.eng.fresh()] = roundUp(rem)
	}
	r.eng.compact(r.coeffs)
	return r
}

// ZeroCrossingError is returned by Div when the divisor's range contains
// zero. Use DivPieces to obtain a piecewise-defined result instead.
type ZeroCrossingError struct {
	Divisor Interval
}

func (e *ZeroCrossingError) Error() string {
	return fmt.Sprintf("affarith: divisor range [%v, %v] crosses zero", e.Divisor.Lo, e.Divisor.Hi)
}

// recip linearizes 1/y over y's range [a, b] (which must not contain 0)
// using the Chebyshev linear approximation: 1/y = alpha*y + zeta + delta*e
// with a fresh noise symbol for the bounded deviation delta.
func recip(y Affine) Affine {
	e := y.eng
	iv := y.Interval()
	a, b := iv.Lo, iv.Hi
	alpha := -1 / (a * b)
	// d(x) = 1/x - alpha*x has its interior extremum at x = ±sqrt(a*b).
	ext := math.Sqrt(a * b)
	if a < 0 {
		ext = -ext
	}
	d := func(x float64) float64 { return 1/x - alpha*x }
	dmin := math.Min(d(a), math.Min(d(b), d(ext)))
	dmax := math.Max(d(a), math.Max(d(b), d(ext)))
	zeta := (dmin + dmax) / 2
	delta := roundUp((dmax - dmin) / 2)
	r := Affine{
		eng:    e,
		c0:     alpha*y.c0 + zeta,
		coeffs: make(map[int]float64, len(y.coeffs)+1),
	}
	for id, c := range y.coeffs {
		r.coeffs[id] = alpha * c
	}
	r.coeffs[e.fresh()] = delta
	e.compact(r.coeffs)
	return r
}

// Div computes a / b. If b's range contains zero it returns a
// *ZeroCrossingError; use DivPieces for a piecewise-defined result.
func (a Affine) Div(b Affine) (Affine, error) {
	a.mustShare(b)
	bi := b.Interval()
	if bi.Lo <= 0 && 0 <= bi.Hi {
		return Affine{}, &ZeroCrossingError{Divisor: bi}
	}
	q := a.Mul(recip(b))
	q.node = &Node{Op: OpDiv, Args: []*Node{a.node, b.node}}
	return q, nil
}

// Piece is one branch of a piecewise-defined division result: the value of
// the quotient when the divisor is restricted to DivisorDomain lies in
// Range (which may be unbounded).
type Piece struct {
	DivisorDomain Interval
	Range         Interval
}

// DivPieces divides a by b even when b's range crosses zero, returning one
// piece per sign of the divisor. The zero end of each divisor domain is
// open, so ranges may be infinite.
func DivPieces(a, b Affine) []Piece {
	a.mustShare(b)
	bi := b.Interval()
	ai := a.Interval()
	if bi.Lo > 0 || bi.Hi < 0 {
		r, _ := idiv(ai, bi)
		return []Piece{{DivisorDomain: bi, Range: r}}
	}
	var out []Piece
	if bi.Lo < 0 {
		d := Interval{bi.Lo, 0}
		out = append(out, Piece{DivisorDomain: d, Range: imulOpen(ai, irecipClipped(d))})
	}
	if bi.Hi > 0 {
		d := Interval{0, bi.Hi}
		out = append(out, Piece{DivisorDomain: d, Range: imulOpen(ai, irecipClipped(d))})
	}
	return out
}
