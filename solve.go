package main

import (
	"fmt"
	"math/big"
)

// Stationary describes one interior stationary point of the curve.
type Stationary struct {
	Iv    Interval // rational isolating interval of the parameter
	Mult  int      // multiplicity as a root of the derivative numerator
	Left  string   // "increasing" or "decreasing" just left of the point
	Right string   // just right of the point
	Role  string   // "max", "min" (local) or "none" (even multiplicity)
}

// Solution is the full certified analysis of a curve.
type Solution struct {
	Curve     Curve
	Eps       *big.Rat
	P         Poly   // derivative numerator (rational coeffs)
	Q         Poly   // square-free part of P
	qseq      []Poly // Sturm sequence of Q
	Constant  bool
	Stats     []Stationary
	Regions   []int // sign of B' on each of the len(Stats)+1 monotone regions
	MaxVal    iv
	MinVal    iv
	MaxParams []Interval // all tied maximizers (endpoints as degenerate intervals)
	MinParams []Interval
}

var (
	ivZero = Interval{ratI(0), ratI(0)}
	ivOne  = Interval{ratI(1), ratI(1)}
	ivFull = Interval{ratI(0), ratI(1)}
)

// solve runs the complete certified analysis.
func solve(c Curve, eps *big.Rat) (*Solution, error) {
	if len(c.Values) < 2 || len(c.Values) > 6 {
		return nil, fmt.Errorf("degree must be 1..5 (got %d control values)", len(c.Values))
	}
	if len(c.Weights) != len(c.Values) {
		return nil, fmt.Errorf("values/weights length mismatch")
	}
	for _, w := range c.Weights {
		if w.Sign() <= 0 {
			return nil, fmt.Errorf("weights must be positive")
		}
	}
	if eps.Sign() <= 0 {
		return nil, fmt.Errorf("epsilon must be positive")
	}
	P, _, _ := c.derivNumerator()
	sol := &Solution{Curve: c, Eps: eps, P: P}
	if P.Zero() {
		sol.Constant = true
		v := c.eval(ratI(0))
		sol.MaxVal = iv{new(big.Rat).Set(v), new(big.Rat).Set(v)}
		sol.MinVal = sol.MaxVal
		sol.MaxParams = []Interval{ivFull}
		sol.MinParams = []Interval{ivFull}
		return sol, nil
	}
	sol.Q = squareFreeP(P)
	roots := isolateInterior(sol.Q, eps)
	stats, regions, err := buildStats(P, sol.Q, roots)
	if err != nil {
		return nil, err
	}
	sol.Stats = stats
	sol.Regions = regions
	return fillExtrema(sol)
}

// buildStats computes multiplicity and monotonicity for each isolated root.
func buildStats(P, Q Poly, roots []Interval) ([]Stationary, []int, error) {
	qseq := sturmSeq(Q)
	_ = qseq
	// region signs: sample rational points between consecutive root intervals
	regions := make([]int, len(roots)+1)
	for i := 0; i <= len(roots); i++ {
		var s *big.Rat
		if len(roots) == 0 {
			s = ratI(1)
			s.Quo(s, ratI(2))
		} else if i == 0 {
			s = avg(ratI(0), roots[0].L)
		} else if i == len(roots) {
			s = avg(roots[len(roots)-1].R, ratI(1))
		} else {
			s = avg(roots[i-1].R, roots[i].L)
		}
		sg := P.eval(s).Sign()
		if sg == 0 {
			return nil, nil, fmt.Errorf("internal: zero sign sample")
		}
		regions[i] = sg
	}
	stats := make([]Stationary, len(roots))
	for i, iv := range roots {
		m := multiplicity(P, Q, iv)
		st := Stationary{Iv: iv, Mult: m}
		st.Left = signWord(regions[i])
		st.Right = signWord(regions[i+1])
		switch {
		case m%2 == 1 && regions[i] > 0 && regions[i+1] < 0:
			st.Role = "max"
		case m%2 == 1 && regions[i] < 0 && regions[i+1] > 0:
			st.Role = "min"
		default:
			st.Role = "none"
		}
		stats[i] = st
	}
	return stats, regions, nil
}

func signWord(s int) string {
	if s > 0 {
		return "increasing"
	}
	return "decreasing"
}

// multiplicity of the unique root of square-free Q inside iv as a root of P:
// the least m such that P^(m)(alpha) != 0. Uses gcd(Q, P^(k)) — exact.
func multiplicity(P, Q Poly, iv Interval) int {
	mult := 1
	d := P
	for k := 1; k <= 5; k++ {
		d = derivP(d)
		if d.Zero() {
			break
		}
		g := gcdP(Q, d)
		if g.Deg() < 1 {
			break
		}
		isRoot := false
		if iv.L.Cmp(iv.R) == 0 {
			isRoot = g.eval(iv.L).Sign() == 0
		} else {
			gseq := sturmSeq(g)
			isRoot = countRoots(gseq, iv.L, iv.R) == 1
		}
		if isRoot {
			mult++
		} else {
			break
		}
	}
	return mult
}

// candidate is a possible global extremum location.
type candidate struct {
	endpoint bool
	endIdx   int // 0 or 1
	rootIdx  int // index into stats, if interior
	paramIv  Interval
	val      *big.Rat // exact value for endpoints
	valIv    iv       // enclosure for interior candidates
	key      string
}

// fillExtrema computes value enclosures, exact tie grouping and global extrema.
func fillExtrema(sol *Solution) (*Solution, error) {
	c, eps := sol.Curve, sol.Eps
	_, N, D := c.derivNumerator()
	qseq := sturmSeq(sol.Q)

	var cands []*candidate
	cands = append(cands, &candidate{endpoint: true, endIdx: 0, paramIv: ivZero,
		val: c.eval(ratI(0))})
	cands = append(cands, &candidate{endpoint: true, endIdx: 1, paramIv: ivOne,
		val: c.eval(ratI(1))})
	for i, st := range sol.Stats {
		if st.Mult%2 == 0 {
			continue // stationary inflection: not an extremum
		}
		cd := &candidate{rootIdx: i, paramIv: st.Iv}
		cd.valIv, cd.paramIv = valueEnclosure(N, D, sol.Q, qseq, cd.paramIv, eps)
		cands = append(cands, cd)
	}

	// Exact tie detection via R(y) = Res_t(Q, N - y D).
	interior := 0
	for _, cd := range cands {
		if !cd.endpoint {
			interior++
		}
	}
	if interior > 0 {
		if err := groupByResultant(c, sol, N, D, qseq, cands); err != nil {
			return nil, err
		}
	} else {
		for _, cd := range cands {
			cd.key = "pt:" + cd.val.RatString()
		}
	}

	// Build groups.
	groups := map[string]*group{}
	var order []string
	for _, cd := range cands {
		g, ok := groups[cd.key]
		if !ok {
			g = &group{}
			groups[cd.key] = g
			order = append(order, cd.key)
		}
		g.members = append(g.members, cd)
	}
	for _, g := range groups {
		g.recompute()
	}
	// Separate distinct groups so their order is decidable.
	separateGroups(groups, c, sol, N, D, qseq)
	// Tighten each group's value enclosure to width <= eps.
	for _, g := range groups {
		g.tighten(eps, c, sol, N, D, qseq)
	}

	maxG, minG := groups[order[0]], groups[order[0]]
	for _, k := range order[1:] {
		g := groups[k]
		if g.valIv.L.Cmp(maxG.valIv.R) > 0 {
			maxG = g
		}
		if g.valIv.R.Cmp(minG.valIv.L) < 0 {
			minG = g
		}
	}
	sol.MaxVal = maxG.valIv
	sol.MinVal = minG.valIv
	sol.MaxParams = maxG.params()
	sol.MinParams = minG.params()
	return sol, nil
}

// group is a set of candidates certified to share the same value.
type group struct {
	members []*candidate
	valIv   iv
}

func (g *group) recompute() {
	lo, hi := g.members[0].bound()
	for _, m := range g.members[1:] {
		l, h := m.bound()
		if l.Cmp(lo) < 0 {
			lo = l
		}
		if h.Cmp(hi) > 0 {
			hi = h
		}
	}
	g.valIv = iv{lo, hi}
}

func (m *candidate) bound() (*big.Rat, *big.Rat) {
	if m.endpoint {
		return m.val, m.val
	}
	return m.valIv.L, m.valIv.R
}

func (g *group) params() []Interval {
	var out []Interval
	for _, m := range g.members {
		out = append(out, m.paramIv)
	}
	return out
}

// tighten refines interior members until the group enclosure width <= eps.
func (g *group) tighten(eps *big.Rat, c Curve, sol *Solution, N, D Poly, qseq []Poly) {
	for new(big.Rat).Sub(g.valIv.R, g.valIv.L).Cmp(eps) > 0 {
		changed := false
		for _, m := range g.members {
			if m.endpoint {
				continue
			}
			m.paramIv = bisectOnce(sol.Q, qseq, m.paramIv)
			m.valIv = valueIv(N, D, m.paramIv)
			changed = true
		}
		if !changed {
			break
		}
		g.recompute()
	}
}

// separateGroups refines interior members until distinct groups have
// disjoint value enclosures, making their order exact.
func separateGroups(groups map[string]*group, c Curve, sol *Solution, N, D Poly, qseq []Poly) {
	for {
		overlapping := false
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		for i := 0; i < len(keys) && !overlapping; i++ {
			for j := i + 1; j < len(keys) && !overlapping; j++ {
				a, b := groups[keys[i]], groups[keys[j]]
				if a.valIv.L.Cmp(b.valIv.R) <= 0 && b.valIv.L.Cmp(a.valIv.R) <= 0 {
					overlapping = true
				}
			}
		}
		if !overlapping {
			return
		}
		for _, g := range groups {
			for _, m := range g.members {
				if m.endpoint {
					continue
				}
				m.paramIv = bisectOnce(sol.Q, qseq, m.paramIv)
				m.valIv = valueIv(N, D, m.paramIv)
			}
			g.recompute()
		}
	}
}

// valueIv encloses B over a parameter interval via interval arithmetic.
func valueIv(N, D Poly, p Interval) iv {
	x := iv{p.L, p.R}
	if p.L.Cmp(p.R) == 0 {
		nv, dv := N.eval(p.L), D.eval(p.L)
		v := new(big.Rat).Quo(nv, dv)
		return iv{v, v}
	}
	nIv := evalPolyIv(N, x)
	dIv := evalPolyIv(D, x)
	if dIv.L.Sign() <= 0 {
		// caller must refine first; should not happen for narrow intervals
		return iv{ratI(-1 << 30), ratI(1 << 30)}
	}
	return iv{new(big.Rat).Quo(nIv.L, dIv.R), new(big.Rat).Quo(nIv.R, dIv.L)}
}

// valueEnclosure refines the parameter interval until the value enclosure
// has width <= eps. Returns the (possibly shrunk) parameter interval too.
func valueEnclosure(N, D, Q Poly, qseq []Poly, p Interval, eps *big.Rat) (iv, Interval) {
	for {
		v := valueIv(N, D, p)
		if p.L.Cmp(p.R) == 0 {
			return v, p
		}
		w := new(big.Rat).Sub(v.R, v.L)
		dIv := evalPolyIv(D, iv{p.L, p.R})
		if w.Cmp(eps) <= 0 && dIv.L.Sign() > 0 {
			return v, p
		}
		p = bisectOnce(Q, qseq, p)
	}
}

// groupByResultant assigns each candidate a group key such that two
// candidates share a key iff their values are exactly equal.
func groupByResultant(c Curve, sol *Solution, N, D Poly, qseq []Poly, cands []*candidate) error {
	R := resultantY(sol.Q, N, D)
	if R.Zero() {
		return fmt.Errorf("internal: zero resultant")
	}
	RSF := squareFreeP(R)
	// All candidate values lie in [min b_i, max b_i]; isolate RSF roots wider.
	lo, hi := c.Values[0], c.Values[0]
	for _, v := range c.Values[1:] {
		if v.Cmp(lo) < 0 {
			lo = v
		}
		if v.Cmp(hi) > 0 {
			hi = v
		}
	}
	a := new(big.Rat).Sub(lo, ratI(1))
	b := new(big.Rat).Add(hi, ratI(1))
	rseq := sturmSeq(RSF)
	var rivs []Interval
	isolateRec(RSF, rseq, a, b, signVar(rseq, a), signVar(rseq, b), &rivs)
	if len(rivs) == 0 {
		return fmt.Errorf("internal: resultant has no roots in range")
	}

	delta := new(big.Rat).Set(sol.Eps)
	for {
		rivs = refineRoots(RSF, rivs, delta)
		pending := false
		for _, cd := range cands {
			if cd.key != "" {
				continue
			}
			if cd.endpoint {
				v := cd.val
				if RSF.eval(v).Sign() != 0 {
					cd.key = "pt:" + v.RatString()
					continue
				}
				// v is a root of RSF: find its isolating interval
				idx := -1
				for i, rv := range rivs {
					if rv.contains(v) {
						idx = i
						break
					}
				}
				if idx < 0 {
					pending = true
					continue
				}
				cd.key = fmt.Sprintf("R:%d", idx)
				continue
			}
			// interior: B(alpha) lies in valIv and is a root of RSF, and
			// every RSF root lies in some isolating interval; so once valIv
			// overlaps exactly one interval, B(alpha) is certified to be
			// the root inside it.
			idx, hits := -1, 0
			for i, rv := range rivs {
				if rv.L.Cmp(cd.valIv.R) <= 0 && cd.valIv.L.Cmp(rv.R) <= 0 {
					idx = i
					hits++
				}
			}
			if hits != 1 {
				cd.paramIv = bisectOnce(sol.Q, qseq, cd.paramIv)
				cd.valIv = valueIv(N, D, cd.paramIv)
				pending = true
				continue
			}
			cd.key = fmt.Sprintf("R:%d", idx)
		}
		if !pending {
			return nil
		}
		delta.Quo(delta, ratI(4))
	}
}
