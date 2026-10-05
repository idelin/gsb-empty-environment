package main

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Kind is the relation of a constraint: Coeff·x + Const (Kind) 0.
type Kind int

const (
	Le Kind = iota // non-strict: <= 0
	Lt             // strict:     <  0
	Eq             // equality:   == 0
)

func (k Kind) String() string {
	switch k {
	case Le:
		return "<="
	case Lt:
		return "<"
	default:
		return "="
	}
}

// Constraint is a dense linear constraint over numVars variables.
// Source holds the sorted indices of the original input constraints whose
// combination produced this constraint (combination evidence).
type Constraint struct {
	Coeff  []*big.Rat
	Const  *big.Rat
	Kind   Kind
	Source []int
}

func zeroRat() *big.Rat { return new(big.Rat) }

func cloneRat(r *big.Rat) *big.Rat { return new(big.Rat).Set(r) }

func newConstraint(numVars int, kind Kind, source []int) *Constraint {
	c := &Constraint{Coeff: make([]*big.Rat, numVars), Const: zeroRat(), Kind: kind}
	for i := range c.Coeff {
		c.Coeff[i] = zeroRat()
	}
	c.SetSource(source)
	return c
}

func (c *Constraint) Clone() *Constraint {
	n := &Constraint{Coeff: make([]*big.Rat, len(c.Coeff)), Const: cloneRat(c.Const), Kind: c.Kind}
	for i, r := range c.Coeff {
		n.Coeff[i] = cloneRat(r)
	}
	n.Source = append([]int(nil), c.Source...)
	return n
}

func (c *Constraint) SetSource(src []int) {
	c.Source = append([]int(nil), src...)
	sort.Ints(c.Source)
	c.Source = uniqInts(c.Source)
}

func uniqInts(in []int) []int {
	out := in[:0]
	prev := -1
	for i, v := range in {
		if i == 0 || v != prev {
			out = append(out, v)
			prev = v
		}
	}
	return out
}

func unionSources(a, b []int) []int {
	merged := append(append([]int(nil), a...), b...)
	sort.Ints(merged)
	return uniqInts(merged)
}

// combine returns s1*a + s2*b componentwise. Strictness: the result is strict
// if either operand is strict (both multipliers must be positive for the
// elimination to be sound; callers guarantee that). Equalities only combine
// with equalities (never happens in FM pairing).
func combine(a, b *Constraint, s1, s2 *big.Rat) *Constraint {
	n := len(a.Coeff)
	kind := Le
	if a.Kind == Lt || b.Kind == Lt {
		kind = Lt
	}
	if a.Kind == Eq && b.Kind == Eq {
		kind = Eq
	}
	out := newConstraint(n, kind, unionSources(a.Source, b.Source))
	for i := 0; i < n; i++ {
		t := new(big.Rat).Mul(a.Coeff[i], s1)
		t.Add(t, new(big.Rat).Mul(b.Coeff[i], s2))
		out.Coeff[i] = t
	}
	out.Const.Mul(a.Const, s1)
	out.Const.Add(out.Const, new(big.Rat).Mul(b.Const, s2))
	return out
}

// addScaled returns c + s*eq (used for equality substitution).
func addScaled(c, eq *Constraint, s *big.Rat) *Constraint {
	out := c.Clone()
	for i := range out.Coeff {
		out.Coeff[i].Add(out.Coeff[i], new(big.Rat).Mul(eq.Coeff[i], s))
	}
	out.Const.Add(out.Const, new(big.Rat).Mul(eq.Const, s))
	out.SetSource(unionSources(c.Source, eq.Source))
	return out
}

func (c *Constraint) isZeroRow() bool {
	for _, r := range c.Coeff {
		if r.Sign() != 0 {
			return false
		}
	}
	return true
}

// tautology reports whether an all-zero-coefficient constraint is always true.
func (c *Constraint) tautology() bool {
	switch c.Kind {
	case Le:
		return c.Const.Sign() <= 0
	case Lt:
		return c.Const.Sign() < 0
	default:
		return c.Const.Sign() == 0
	}
}

// normalize scales the constraint to a primitive integer representation
// (positive scaling only, so <= and < keep their direction). Equalities are
// additionally sign-flipped so the first nonzero coefficient is positive.
func (c *Constraint) normalize() {
	lcm := big.NewInt(1)
	g := new(big.Int)
	tmp := new(big.Int)
	all := append(append([]*big.Rat{}, c.Coeff...), c.Const)
	for _, r := range all {
		d := r.Denom()
		if d.Cmp(big.NewInt(1)) != 0 {
			tmp.GCD(nil, nil, lcm, d)
			lcm.Div(lcm, tmp)
			lcm.Mul(lcm, d)
		}
	}
	for _, r := range all {
		q := new(big.Int).Quo(lcm, r.Denom())
		tmp.Mul(r.Num(), q)
		tmp.Abs(tmp)
		if tmp.Sign() != 0 {
			if g.Sign() == 0 {
				g.Set(tmp)
			} else {
				g.GCD(nil, nil, g, tmp)
			}
		}
	}
	if g.Sign() == 0 {
		return // all zero, nothing to scale
	}
	// scale = lcm / g  (positive): r * lcm is integral, dividing by the gcd
	// yields the primitive integer vector
	scale := new(big.Rat).SetFrac(lcm, g)
	for _, r := range all {
		r.Mul(r, scale)
	}
	if c.Kind == Eq {
		for _, r := range c.Coeff {
			if r.Sign() != 0 {
				if r.Sign() < 0 {
					for _, q := range all {
						q.Neg(q)
					}
				}
				break
			}
		}
	}
}

// key is the canonical identity used for dedup (after normalize).
func (c *Constraint) key() string {
	var sb strings.Builder
	sb.WriteString(c.Kind.String())
	for _, r := range c.Coeff {
		sb.WriteString("|")
		sb.WriteString(r.RatString())
	}
	sb.WriteString("|")
	sb.WriteString(c.Const.RatString())
	return sb.String()
}

// negate returns the logical negation of c (used for redundancy testing).
// Le -> Lt with negated body; Lt -> Le with negated body.
func (c *Constraint) negate() *Constraint {
	out := c.Clone()
	for i := range out.Coeff {
		out.Coeff[i].Neg(out.Coeff[i])
	}
	out.Const.Neg(out.Const)
	if c.Kind == Le {
		out.Kind = Lt
	} else if c.Kind == Lt {
		out.Kind = Le
	}
	return out
}

// closureSatisfiedAt checks the constraint with strictness ignored
// (strict inequalities treated as non-strict).
func (c *Constraint) closureSatisfiedAt(point []*big.Rat) bool {
	v := c.eval(point)
	if c.Kind == Eq {
		return v.Sign() == 0
	}
	return v.Sign() <= 0
}

// eval computes Coeff·point + Const.
func (c *Constraint) eval(point []*big.Rat) *big.Rat {
	acc := cloneRat(c.Const)
	for i, r := range c.Coeff {
		if i < len(point) && point[i] != nil && r.Sign() != 0 {
			acc.Add(acc, new(big.Rat).Mul(r, point[i]))
		}
	}
	return acc
}

// satisfiedAt reports whether the constraint holds at the given point,
// respecting strictness.
func (c *Constraint) satisfiedAt(point []*big.Rat) bool {
	v := c.eval(point)
	switch c.Kind {
	case Le:
		return v.Sign() <= 0
	case Lt:
		return v.Sign() < 0
	default:
		return v.Sign() == 0
	}
}

func (c *Constraint) String() string {
	parts := []string{}
	for i, r := range c.Coeff {
		if r.Sign() != 0 {
			parts = append(parts, fmt.Sprintf("%s*x%d", r.RatString(), i))
		}
	}
	body := strings.Join(parts, " + ")
	if body == "" {
		body = "0"
	}
	return fmt.Sprintf("%s + %s %s 0   [src=%v]", body, c.Const.RatString(), c.Kind, c.Source)
}
