// Package affine implements affine arithmetic with dependency tracking.
//
// A Form represents c + sum_i coefs[i] * e_i, where each e_i is an
// independent noise symbol in [-1, 1]. Noise symbols introduced by
// NewVar model input variables: the same variable reused in an
// expression shares its symbol, so correlated terms cancel exactly
// (x - x == 0). Nonlinear operations (Mul, Recip) append conservative
// remainder symbols whose count is bounded by the environment budget;
// once the budget is exhausted, remainders are merged conservatively.
package affine

import (
	"math"
	"sort"
)

// Env allocates noise symbols and holds the remainder budget.
// maxRemainder <= 0 means an unbounded number of remainder symbols.
type Env struct {
	next   int
	maxRem int
}

// NewEnv creates an environment. maxRemainder caps how many nonlinear
// remainder symbols a single form may carry; excess is merged.
func NewEnv(maxRemainder int) *Env {
	return &Env{maxRem: maxRemainder}
}

func (e *Env) newID() int {
	id := e.next
	e.next++
	return id
}

// Form is an affine form: Center + sum Coefs[id] * e_id.
// rem marks which ids are nonlinear remainder symbols.
type Form struct {
	env   *Env
	c     float64
	coefs map[int]float64
	rem   map[int]bool
}

// Const returns a constant form.
func (e *Env) Const(v float64) Form {
	return Form{env: e, c: v}
}

// Var returns a fresh input variable with range [-1, 1].
func (e *Env) Var() Form {
	id := e.newID()
	return Form{env: e, coefs: map[int]float64{id: 1}}
}

// NumRemainder reports how many nonlinear remainder symbols f carries.
func (f Form) NumRemainder() int { return len(f.rem) }

// Range returns the conservative interval enclosure of f.
// The radius is accumulated with outward rounding so that exact
// re-computation (see Check) never exceeds the returned bounds.
func (f Form) Range() (lo, hi float64) {
	r := 0.0
	for _, c := range f.coefs {
		r = math.Nextafter(r+math.Abs(c), math.Inf(1))
	}
	if r == 0 {
		return f.c, f.c
	}
	return math.Nextafter(f.c-r, math.Inf(-1)), math.Nextafter(f.c+r, math.Inf(1))
}

func (f Form) clone() Form {
	g := Form{env: f.env, c: f.c}
	if f.coefs != nil {
		g.coefs = make(map[int]float64, len(f.coefs))
		for id, c := range f.coefs {
			g.coefs[id] = c
		}
	}
	if f.rem != nil {
		g.rem = make(map[int]bool, len(f.rem))
		for id := range f.rem {
			g.rem[id] = true
		}
	}
	return g
}

func sameEnv(f, g Form) {
	if f.env != g.env {
		panic("affine: operands belong to different environments")
	}
}

// tidy drops zero coefficients and stale remainder flags.
func (f *Form) tidy() {
	for id, c := range f.coefs {
		if c == 0 {
			delete(f.coefs, id)
			delete(f.rem, id)
		}
	}
}

// addRem appends a conservative remainder of the given magnitude,
// honouring the environment budget by merging when it is exhausted.
func (f *Form) addRem(mag float64) {
	if mag == 0 {
		return
	}
	if f.coefs == nil {
		f.coefs = make(map[int]float64)
	}
	if f.rem == nil {
		f.rem = make(map[int]bool)
	}
	if f.env.maxRem <= 0 || len(f.rem) < f.env.maxRem {
		id := f.env.newID()
		f.coefs[id] = mag
		f.rem[id] = true
		return
	}
	// Budget exhausted: merge into an existing remainder symbol.
	// |a|+mag on one symbol conservatively covers a*e1 + mag*e2.
	id := f.smallestRem()
	f.coefs[id] = math.Abs(f.coefs[id]) + mag
}

func (f *Form) smallestRem() int {
	ids := make([]int, 0, len(f.rem))
	for id := range f.rem {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids[0]
}

// consolidate merges remainder symbols until the budget is met.
func (f *Form) consolidate() {
	if f.env.maxRem <= 0 {
		return
	}
	for len(f.rem) > f.env.maxRem {
		// Merge the two smallest-magnitude remainder symbols.
		ids := make([]int, 0, len(f.rem))
		for id := range f.rem {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			ai, aj := math.Abs(f.coefs[ids[i]]), math.Abs(f.coefs[ids[j]])
			if ai != aj {
				return ai < aj
			}
			return ids[i] < ids[j]
		})
		a, b := ids[0], ids[1]
		f.coefs[a] = math.Abs(f.coefs[a]) + math.Abs(f.coefs[b])
		delete(f.coefs, b)
		delete(f.rem, b)
	}
}

// Add returns f + g with shared symbols combined exactly.
func (f Form) Add(g Form) Form {
	sameEnv(f, g)
	h := f.clone()
	h.c += g.c
	if h.coefs == nil && len(g.coefs) > 0 {
		h.coefs = make(map[int]float64)
	}
	if h.rem == nil && len(g.rem) > 0 {
		h.rem = make(map[int]bool)
	}
	for id, c := range g.coefs {
		h.coefs[id] += c
	}
	for id := range g.rem {
		if _, ok := h.coefs[id]; ok {
			h.rem[id] = true
		}
	}
	h.tidy()
	h.consolidate()
	return h
}

// Neg returns -f.
func (f Form) Neg() Form {
	h := f.clone()
	h.c = -h.c
	for id, c := range h.coefs {
		h.coefs[id] = -c
	}
	return h
}

// Sub returns f - g; identical symbols cancel exactly.
func (f Form) Sub(g Form) Form {
	sameEnv(f, g)
	return f.Add(g.Neg())
}

// Scale returns f multiplied by the constant s.
func (f Form) Scale(s float64) Form {
	h := f.clone()
	h.c *= s
	for id, c := range h.coefs {
		h.coefs[id] = c * s
	}
	h.tidy()
	return h
}

// Shift returns f plus the constant s.
func (f Form) Shift(s float64) Form {
	h := f.clone()
	h.c += s
	return h
}

func sumAbs(m map[int]float64) float64 {
	s := 0.0
	for _, c := range m {
		s += math.Abs(c)
	}
	return s
}

// Mul returns f * g. The product of the non-constant parts,
// (sum a_i e_i)(sum b_j e_j), is bounded by Ra*Rb where
// Ra = sum|a_i|, Rb = sum|b_j|, and added as one remainder symbol.
func (f Form) Mul(g Form) Form {
	sameEnv(f, g)
	ra, rb := sumAbs(f.coefs), sumAbs(g.coefs)
	h := Form{env: f.env, c: f.c * g.c}
	n := len(f.coefs) + len(g.coefs)
	if n > 0 {
		h.coefs = make(map[int]float64, n)
		h.rem = make(map[int]bool)
	}
	for id, a := range f.coefs {
		h.coefs[id] = a * g.c
		if f.rem[id] {
			h.rem[id] = true
		}
	}
	for id, b := range g.coefs {
		h.coefs[id] += f.c * b
		if g.rem[id] {
			h.rem[id] = true
		}
	}
	h.tidy()
	h.addRem(ra * rb)
	h.consolidate()
	return h
}
