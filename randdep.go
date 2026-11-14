// Package netsec provides random-dependency analysis for privacy-mechanism
// test hosts. A program is a finite set of linear expressions over secret
// inputs and random variables; every variable carries an independent
// identity. The solver searches for a minimum-size combination of observable
// outputs whose rational linear combination cancels every random term while
// preserving at least one secret term — the algebraic signature of noise
// cancellation caused by random-seed reuse.
//
// Variables are keyed strictly by identity: two variables that happen to
// hold the same sampled value are never treated as the same random source.
package netsec

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Expr is a linear expression over variable identities with rational
// coefficients. Terms maps a variable identity to its coefficient; zero
// coefficients are never stored.
type Expr struct {
	Terms map[string]*big.Rat
}

// Term builds an expression from a single variable identity with
// coefficient 1.
func Term(id string) Expr {
	return Expr{Terms: map[string]*big.Rat{id: big.NewRat(1, 1)}}
}

// Scale multiplies every coefficient by c.
func (e Expr) Scale(c *big.Rat) Expr {
	out := Expr{Terms: make(map[string]*big.Rat, len(e.Terms))}
	for id, k := range e.Terms {
		v := new(big.Rat).Mul(k, c)
		if v.Sign() != 0 {
			out.Terms[id] = v
		}
	}
	return out
}

// Add returns the term-wise sum of two expressions.
func (e Expr) Add(o Expr) Expr {
	out := Expr{Terms: make(map[string]*big.Rat, len(e.Terms)+len(o.Terms))}
	for id, k := range e.Terms {
		out.Terms[id] = new(big.Rat).Set(k)
	}
	for id, k := range o.Terms {
		if cur, ok := out.Terms[id]; ok {
			cur.Add(cur, k)
			if cur.Sign() == 0 {
				delete(out.Terms, id)
			}
		} else {
			out.Terms[id] = new(big.Rat).Set(k)
		}
	}
	return out
}

// coeff returns a copy of the coefficient of id, or zero. The copy matters:
// elimination mutates the matrices built from these values in place, and
// must never corrupt the program's expressions.
func (e Expr) coeff(id string) *big.Rat {
	if k, ok := e.Terms[id]; ok {
		return new(big.Rat).Set(k)
	}
	return new(big.Rat)
}

// isZero reports whether every coefficient is zero.
func (e Expr) isZero() bool {
	return len(e.Terms) == 0
}

func (e Expr) String() string {
	if len(e.Terms) == 0 {
		return "0"
	}
	ids := make([]string, 0, len(e.Terms))
	for id := range e.Terms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		if name == "" {
			name = "1"
		}
		parts = append(parts, fmt.Sprintf("%s·%s", e.Terms[id].RatString(), name))
	}
	return strings.Join(parts, " + ")
}

// Output is one observable expression of the program under test.
type Output struct {
	Name string
	Expr Expr
}

// Program is a finite linear-expression program: disjoint sets of secret
// and random variable identities, plus the observable outputs.
type Program struct {
	Secrets []string
	Randoms []string
	Outputs []Output
}

// validate checks structural invariants and returns the variable kind map
// (true = secret, false = random).
func (p *Program) validate(maxOutputs int) (map[string]bool, error) {
	kind := make(map[string]bool, len(p.Secrets)+len(p.Randoms))
	for _, id := range p.Secrets {
		if id == "" {
			return nil, fmt.Errorf("randdep: empty secret identity")
		}
		if _, dup := kind[id]; dup {
			return nil, fmt.Errorf("randdep: duplicate variable identity %q", id)
		}
		kind[id] = true
	}
	for _, id := range p.Randoms {
		if id == "" {
			return nil, fmt.Errorf("randdep: empty random identity")
		}
		if _, dup := kind[id]; dup {
			return nil, fmt.Errorf("randdep: duplicate variable identity %q", id)
		}
		kind[id] = false
	}
	if len(p.Outputs) == 0 {
		return nil, fmt.Errorf("randdep: program has no outputs")
	}
	if len(p.Outputs) > maxOutputs {
		return nil, fmt.Errorf("randdep: %d outputs exceed limit %d", len(p.Outputs), maxOutputs)
	}
	seen := make(map[string]bool, len(p.Outputs))
	for _, o := range p.Outputs {
		if o.Name == "" {
			return nil, fmt.Errorf("randdep: output with empty name")
		}
		if seen[o.Name] {
			return nil, fmt.Errorf("randdep: duplicate output name %q", o.Name)
		}
		seen[o.Name] = true
		for id := range o.Expr.Terms {
			if id == "" {
				continue // constant term
			}
			if _, ok := kind[id]; !ok {
				return nil, fmt.Errorf("randdep: output %q references undeclared variable %q", o.Name, id)
			}
		}
	}
	return kind, nil
}
