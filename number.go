package algebraic

import (
	"errors"
	"fmt"
	"math/big"
)

type exprKind int

const (
	exprRational exprKind = iota
	exprNeg
	exprAdd
	exprSub
	exprMul
	exprDiv
	exprSqrt
)

type expr struct {
	kind        exprKind
	rat         *big.Rat
	left, right *expr
}

// Number is an exactly represented real algebraic number expression.
type Number struct {
	expr *expr
}

// Polynomial is a monic rational-coefficient polynomial.
type Polynomial struct {
	coeffs poly
}

// Coefficients returns coefficients from constant term to leading term.
func (p Polynomial) Coefficients() []*big.Rat {
	out := make([]*big.Rat, len(p.coeffs))
	for i, c := range p.coeffs {
		out[i] = cloneRat(c)
	}
	return out
}

func (p Polynomial) String() string {
	return polyString(p.coeffs)
}

// Rational constructs a rational algebraic number.
func Rational(x *big.Rat) *Number {
	return &Number{expr: &expr{kind: exprRational, rat: cloneRat(x)}}
}

// Rat constructs num/den as an algebraic number.
func Rat(num, den int64) *Number {
	return Rational(big.NewRat(num, den))
}

// Int constructs an integer algebraic number.
func Int(n int64) *Number {
	return Rat(n, 1)
}

func Neg(a *Number) *Number {
	return &Number{expr: &expr{kind: exprNeg, left: a.expr}}
}

func Add(a, b *Number) *Number {
	return &Number{expr: &expr{kind: exprAdd, left: a.expr, right: b.expr}}
}

func Sub(a, b *Number) *Number {
	return &Number{expr: &expr{kind: exprSub, left: a.expr, right: b.expr}}
}

func Mul(a, b *Number) *Number {
	return &Number{expr: &expr{kind: exprMul, left: a.expr, right: b.expr}}
}

// Div constructs a/b. It returns an error when b is exactly zero.
func Div(a, b *Number) (*Number, error) {
	if b.Sign() == 0 {
		return nil, errors.New("division by zero algebraic number")
	}
	return &Number{expr: &expr{kind: exprDiv, left: a.expr, right: b.expr}}, nil
}

// Sqrt constructs the non-negative square root of a.
func Sqrt(a *Number) (*Number, error) {
	if a.Sign() < 0 {
		return nil, errors.New("square root of a negative algebraic number")
	}
	return &Number{expr: &expr{kind: exprSqrt, left: a.expr}}, nil
}

// MustSqrt is Sqrt for expressions known to be non-negative.
func MustSqrt(a *Number) *Number {
	out, err := Sqrt(a)
	if err != nil {
		panic(err)
	}
	return out
}

// MustDiv is Div for expressions known to have a non-zero denominator.
func MustDiv(a, b *Number) *Number {
	out, err := Div(a, b)
	if err != nil {
		panic(err)
	}
	return out
}

// Sign returns -1, 0, or 1 according to the exact value.
func (n *Number) Sign() int {
	return n.Compare(Int(0))
}

// Compare returns -1, 0, or 1 when n is less than, equal to, or greater than other.
func (n *Number) Compare(other *Number) int {
	left := n.canonical()
	right := other.canonical()
	if polyEqual(left.minimal, right.minimal) && left.rootIndex == right.rootIndex {
		return 0
	}
	leftInterval := left.interval
	rightInterval := right.interval
	for {
		if leftInterval.Hi.Cmp(rightInterval.Lo) < 0 {
			return -1
		}
		if rightInterval.Hi.Cmp(leftInterval.Lo) < 0 {
			return 1
		}
		leftInterval = refineRootInterval(left.minimal, leftInterval)
		rightInterval = refineRootInterval(right.minimal, rightInterval)
	}
}

// Equal reports exact algebraic equality.
func (n *Number) Equal(other *Number) bool {
	return n.Compare(other) == 0
}

// MinimalPolynomial returns the monic irreducible polynomial of n over Q.
func (n *Number) MinimalPolynomial() Polynomial {
	canonical := n.canonical()
	return Polynomial{coeffs: clonePoly(canonical.minimal)}
}

// IsolatingInterval returns the canonical rational interval isolating n's root.
func (n *Number) IsolatingInterval() Interval {
	canonical := n.canonical()
	return cloneInterval(canonical.interval)
}

func (n *Number) String() string {
	return exprString(n.expr)
}

type canonicalNumber struct {
	minimal   poly
	rootIndex int
	interval  Interval
}

func (n *Number) canonical() canonicalNumber {
	annihilating := annihilateExpr(n.expr)
	factors := factorPolynomial(annihilating)
	var minimal poly
	for bits := uint(1); ; bits++ {
		interval := evalNumberInterval(n, bits)
		candidates := make([]poly, 0, len(factors))
		for _, factor := range factors {
			factorInterval := evalPolyInterval(factor, interval)
			if intervalContainsZero(factorInterval) {
				candidates = append(candidates, factor)
			}
		}
		if len(candidates) == 1 {
			minimal = candidates[0]
			break
		}
	}
	roots := isolateRealRoots(minimal)
	for bits := uint(1); ; bits++ {
		interval := evalNumberInterval(n, bits)
		candidates := make([]int, 0, len(roots))
		for i, root := range roots {
			if intervalIntersects(interval, root) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 1 {
			index := candidates[0]
			return canonicalNumber{
				minimal:   clonePoly(minimal),
				rootIndex: index,
				interval:  cloneInterval(roots[index]),
			}
		}
		for i := range roots {
			roots[i] = refineRootInterval(minimal, roots[i])
		}
	}
}

func annihilateExpr(e *expr) poly {
	switch e.kind {
	case exprRational:
		return poly{new(big.Rat).Neg(e.rat), ratInt(1)}
	case exprNeg:
		return polyMonic(polyNegX(annihilateExpr(e.left)))
	case exprAdd:
		return resultantAdd(annihilateExpr(e.left), annihilateExpr(e.right))
	case exprSub:
		return resultantAdd(annihilateExpr(e.left), polyNegX(annihilateExpr(e.right)))
	case exprMul:
		return resultantMul(annihilateExpr(e.left), annihilateExpr(e.right))
	case exprDiv:
		return resultantMul(annihilateExpr(e.left), polyReverse(annihilateExpr(e.right)))
	case exprSqrt:
		return polyMonic(polyComposeSquare(annihilateExpr(e.left)))
	default:
		panic("unknown expression kind")
	}
}

func exprString(e *expr) string {
	switch e.kind {
	case exprRational:
		return e.rat.RatString()
	case exprNeg:
		return "(-" + exprString(e.left) + ")"
	case exprAdd:
		return "(" + exprString(e.left) + " + " + exprString(e.right) + ")"
	case exprSub:
		return "(" + exprString(e.left) + " - " + exprString(e.right) + ")"
	case exprMul:
		return "(" + exprString(e.left) + " * " + exprString(e.right) + ")"
	case exprDiv:
		return "(" + exprString(e.left) + " / " + exprString(e.right) + ")"
	case exprSqrt:
		return "sqrt(" + exprString(e.left) + ")"
	default:
		return fmt.Sprintf("<unknown expression %d>", e.kind)
	}
}
