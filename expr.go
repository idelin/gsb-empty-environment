// Package domain implements an exact domain analyzer for real-valued
// expressions built from rational constants, the four arithmetic
// operations and rational (p/q) powers of a single variable ranging
// over a given rational interval.
//
// All reasoning is exact (math/big.Rat); no floating point is ever
// used to guess boundaries. Only exactly decidable real branches are
// handled: an even-denominator exponent rejects a negative base, and
// zero to a negative power is undefined. Parts of the expression whose
// domain cannot be decided exactly are reported as unsupported instead
// of being guessed.
package domain

import "math/big"

// Kind identifies the node type of an Expr.
type Kind int

const (
	KConst Kind = iota // rational constant, Val set
	KVar               // the variable x
	KAdd               // L + R
	KSub               // L - R
	KMul               // L * R
	KDiv               // L / R
	KPow               // L ^ Exp, Exp a rational constant in lowest terms
)

// Expr is an expression tree. Exponents of KPow nodes are always
// rational constants kept in lowest terms (big.Rat normalizes), so
// 2/4 and 1/2 are the same exponent. Nested powers are kept as nested
// nodes and are never merged into a single power.
type Expr struct {
	Kind Kind
	Val  *big.Rat // KConst only
	Exp  *big.Rat // KPow only: reduced rational exponent
	L, R *Expr    // operands; KPow uses L as the base
}

// Const builds a rational constant node.
func Const(v *big.Rat) *Expr { return &Expr{Kind: KConst, Val: v} }

// Var builds the variable node.
func Var() *Expr { return &Expr{Kind: KVar} }

// Pow builds a power node with a reduced rational exponent.
func Pow(base *Expr, exp *big.Rat) *Expr {
	return &Expr{Kind: KPow, L: base, Exp: new(big.Rat).Set(exp)}
}

func (e *Expr) String() string {
	switch e.Kind {
	case KConst:
		return e.Val.RatString()
	case KVar:
		return "x"
	case KAdd:
		return "(" + e.L.String() + " + " + e.R.String() + ")"
	case KSub:
		return "(" + e.L.String() + " - " + e.R.String() + ")"
	case KMul:
		return "(" + e.L.String() + " * " + e.R.String() + ")"
	case KDiv:
		return "(" + e.L.String() + " / " + e.R.String() + ")"
	case KPow:
		return "(" + e.L.String() + ")^(" + e.Exp.RatString() + ")"
	}
	return "?"
}
