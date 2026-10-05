package affarith

import (
	"fmt"
	"math"
	"math/big"
)

// Certificate is a checkable claim that the expression recorded in an
// Affine's DAG is bounded by [Lower, Upper] over Domain. It can be
// re-checked independently with Verify, using only the public data below
// plus the expression DAG.
type Certificate struct {
	Domain   []Interval // input variable domains, indexed by variable id
	Lower    float64    // claimed lower bound
	Upper    float64    // claimed upper bound
	GridSize int        // rational grid cells per variable used by Verify
}

// Certify exports the interval enclosure of a as a Certificate over the
// engine's variable domains. grid is the number of rational grid cells per
// variable that Verify will use (values < 1 are treated as 1).
func (e *Engine) Certify(a Affine, grid int) Certificate {
	if a.eng != e {
		panic("affarith: affine form belongs to a different engine")
	}
	iv := a.Interval()
	dom := make([]Interval, len(e.vars))
	copy(dom, e.vars)
	return Certificate{Domain: dom, Lower: iv.Lo, Upper: iv.Hi, GridSize: grid}
}

// ratOf converts a float64 to its exact binary rational value.
func ratOf(f float64) *big.Rat {
	r := new(big.Rat).SetFloat64(f)
	if r == nil {
		panic("affarith: non-finite value in certificate")
	}
	return r
}

// Verify independently checks a certificate against the expression DAG.
// The domain is split into a small rational grid of boxes; on every box
// the expression is evaluated with independent (naive) interval rules in
// exact rational arithmetic, which is a sound enclosure of the
// expression's values on that box. The certificate holds iff every box
// enclosure lies inside [Lower, Upper]. Finer grids give sharper boxes, so
// any sound claim is verified for a fine enough grid, while unsound
// (narrowed) claims are rejected.
func Verify(c Certificate, root *Node) error {
	if root == nil {
		return fmt.Errorf("affarith: nil expression")
	}
	if !(c.Lower <= c.Upper) {
		return fmt.Errorf("affarith: empty claimed bounds [%v, %v]", c.Lower, c.Upper)
	}
	n := c.GridSize
	if n < 1 {
		n = 1
	}
	cells := make([][]ratIv, len(c.Domain))
	for i, d := range c.Domain {
		lo, hi := ratOf(d.Lo), ratOf(d.Hi)
		w := new(big.Rat).Sub(hi, lo)
		w.Quo(w, new(big.Rat).SetInt64(int64(n)))
		for k := 0; k < n; k++ {
			a := new(big.Rat).Add(lo, new(big.Rat).Mul(w, new(big.Rat).SetInt64(int64(k))))
			b := new(big.Rat).Add(a, w)
			cells[i] = append(cells[i], ratIv{a, b})
		}
	}
	lo, hi := ratOf(c.Lower), ratOf(c.Upper)
	idx := make([]int, len(cells))
	for {
		box := make([]ratIv, len(cells))
		for i := range cells {
			box[i] = cells[i][idx[i]]
		}
		iv, err := evalRatInterval(root, box)
		if err != nil {
			return err
		}
		if iv.lo.Cmp(lo) < 0 || iv.hi.Cmp(hi) > 0 {
			lf, _ := iv.lo.Float64()
			hf, _ := iv.hi.Float64()
			return fmt.Errorf("affarith: certificate violated on grid box %v: independent enclosure [%v, %v] escapes claimed bounds [%v, %v]",
				box, lf, hf, c.Lower, c.Upper)
		}
		// Odometer-style increment over the grid.
		i := 0
		for {
			if i == len(idx) {
				return nil // whole grid checked
			}
			idx[i]++
			if idx[i] < len(cells[i]) {
				break
			}
			idx[i] = 0
			i++
		}
	}
}

// ratIv is an interval with exact rational endpoints.
type ratIv struct {
	lo, hi *big.Rat
}

func (a ratIv) String() string {
	lf, _ := a.lo.Float64()
	hf, _ := a.hi.Float64()
	return fmt.Sprintf("[%v, %v]", lf, hf)
}

func radd(a, b ratIv) ratIv {
	return ratIv{new(big.Rat).Add(a.lo, b.lo), new(big.Rat).Add(a.hi, b.hi)}
}

func rsub(a, b ratIv) ratIv {
	return ratIv{new(big.Rat).Sub(a.lo, b.hi), new(big.Rat).Sub(a.hi, b.lo)}
}

func rmul(a, b ratIv) ratIv {
	prods := [4]*big.Rat{
		new(big.Rat).Mul(a.lo, b.lo), new(big.Rat).Mul(a.lo, b.hi),
		new(big.Rat).Mul(a.hi, b.lo), new(big.Rat).Mul(a.hi, b.hi),
	}
	lo, hi := prods[0], prods[0]
	for _, p := range prods[1:] {
		if p.Cmp(lo) < 0 {
			lo = p
		}
		if p.Cmp(hi) > 0 {
			hi = p
		}
	}
	return ratIv{lo, hi}
}

func rdiv(a, b ratIv) (ratIv, error) {
	if b.lo.Sign() <= 0 && b.hi.Sign() >= 0 {
		return ratIv{}, fmt.Errorf("affarith: divisor box %v contains zero", b)
	}
	quots := [4]*big.Rat{
		new(big.Rat).Quo(a.lo, b.lo), new(big.Rat).Quo(a.lo, b.hi),
		new(big.Rat).Quo(a.hi, b.lo), new(big.Rat).Quo(a.hi, b.hi),
	}
	lo, hi := quots[0], quots[0]
	for _, q := range quots[1:] {
		if q.Cmp(lo) < 0 {
			lo = q
		}
		if q.Cmp(hi) > 0 {
			hi = q
		}
	}
	return ratIv{lo, hi}, nil
}

// evalRatInterval evaluates the DAG with independent interval rules in
// exact rational arithmetic: every occurrence of a variable is treated as
// an independent input, so correlations are lost, but the result is a
// sound enclosure of the expression's range over the box.
func evalRatInterval(n *Node, box []ratIv) (ratIv, error) {
	switch n.Op {
	case OpVar:
		if n.Var < 0 || n.Var >= len(box) {
			return ratIv{}, fmt.Errorf("affarith: variable %d out of domain", n.Var)
		}
		return box[n.Var], nil
	case OpConst:
		v := ratOf(n.Value)
		return ratIv{v, v}, nil
	}
	if len(n.Args) != 2 {
		return ratIv{}, fmt.Errorf("affarith: malformed %s node", n.Op)
	}
	l, err := evalRatInterval(n.Args[0], box)
	if err != nil {
		return ratIv{}, err
	}
	r, err := evalRatInterval(n.Args[1], box)
	if err != nil {
		return ratIv{}, err
	}
	switch n.Op {
	case OpAdd:
		return radd(l, r), nil
	case OpSub:
		return rsub(l, r), nil
	case OpMul:
		return rmul(l, r), nil
	case OpDiv:
		return rdiv(l, r)
	}
	return ratIv{}, fmt.Errorf("affarith: unknown op %d", n.Op)
}

// evalInterval evaluates the DAG with independent (naive) float64 interval
// arithmetic. Exported for tests and diagnostics; Verify uses the exact
// rational variant above.
func evalInterval(n *Node, dom []Interval) Interval {
	switch n.Op {
	case OpVar:
		if n.Var < 0 || n.Var >= len(dom) {
			return Interval{math.Inf(-1), math.Inf(1)}
		}
		return dom[n.Var]
	case OpConst:
		return Interval{n.Value, n.Value}
	}
	l := evalInterval(n.Args[0], dom)
	r := evalInterval(n.Args[1], dom)
	switch n.Op {
	case OpAdd:
		return iadd(l, r)
	case OpSub:
		return isub(l, r)
	case OpMul:
		return imul(l, r)
	case OpDiv:
		if q, ok := idiv(l, r); ok {
			return q
		}
		return Interval{math.Inf(-1), math.Inf(1)}
	}
	return Interval{math.Inf(-1), math.Inf(1)}
}
