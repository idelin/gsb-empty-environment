package netsec

// Lit is a Boolean literal. 0 is false, 1 is true, a positive value is a
// variable, and its negative is the negated variable.
type Lit int

const (
	litFalse Lit = 0
	litTrue  Lit = 1
)

func neg(l Lit) Lit {
	switch l {
	case litFalse:
		return litTrue
	case litTrue:
		return litFalse
	default:
		return -l
	}
}

type cnfBuilder struct {
	nextVar int
	clauses [][]Lit
	unsat   bool
}

func newCNF() *cnfBuilder {
	return &cnfBuilder{nextVar: 2}
}

func (c *cnfBuilder) newVar() Lit {
	v := Lit(c.nextVar)
	c.nextVar++
	return v
}

func (c *cnfBuilder) maxVar() int {
	return c.nextVar - 1
}

// addClause adds a clause after removing constants and duplicate literals.
func (c *cnfBuilder) addClause(lits ...Lit) {
	if c.unsat {
		return
	}
	seen := make(map[Lit]struct{}, len(lits))
	clean := make([]Lit, 0, len(lits))
	for _, l := range lits {
		switch l {
		case litTrue:
			return
		case litFalse:
			continue
		}
		if _, ok := seen[-l]; ok {
			return
		}
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		clean = append(clean, l)
	}
	if len(clean) == 0 {
		c.unsat = true
		return
	}
	c.clauses = append(c.clauses, clean)
}

func (c *cnfBuilder) assert(l Lit) {
	switch l {
	case litTrue:
		return
	case litFalse:
		c.unsat = true
	default:
		c.addClause(l)
	}
}

func (c *cnfBuilder) and(a, b Lit) Lit {
	if a == litFalse || b == litFalse || a == neg(b) {
		return litFalse
	}
	if a == litTrue {
		return b
	}
	if b == litTrue || a == b {
		return a
	}
	z := c.newVar()
	c.addClause(neg(z), a)
	c.addClause(neg(z), b)
	c.addClause(z, neg(a), neg(b))
	return z
}

func (c *cnfBuilder) or(a, b Lit) Lit {
	if a == litTrue || b == litTrue || a == neg(b) {
		return litTrue
	}
	if a == litFalse {
		return b
	}
	if b == litFalse || a == b {
		return a
	}
	z := c.newVar()
	c.addClause(z, neg(a))
	c.addClause(z, neg(b))
	c.addClause(neg(z), a, b)
	return z
}

func (c *cnfBuilder) xor(a, b Lit) Lit {
	if a == b {
		return litFalse
	}
	if a == neg(b) {
		return litTrue
	}
	if a == litFalse {
		return b
	}
	if b == litFalse {
		return a
	}
	if a == litTrue {
		return neg(b)
	}
	if b == litTrue {
		return neg(a)
	}
	z := c.newVar()
	c.addClause(neg(a), neg(b), neg(z))
	c.addClause(a, b, neg(z))
	c.addClause(a, neg(b), z)
	c.addClause(neg(a), b, z)
	return z
}

func (c *cnfBuilder) eq(a, b Lit) Lit {
	return neg(c.xor(a, b))
}

func (c *cnfBuilder) andAll(xs []Lit) Lit {
	z := litTrue
	for _, x := range xs {
		z = c.and(z, x)
		if z == litFalse {
			return z
		}
	}
	return z
}

func (c *cnfBuilder) orAll(xs []Lit) Lit {
	z := litFalse
	for _, x := range xs {
		z = c.or(z, x)
		if z == litTrue {
			return z
		}
	}
	return z
}

func boolToLit(v bool) Lit {
	if v {
		return litTrue
	}
	return litFalse
}
