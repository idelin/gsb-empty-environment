package ratbez

import (
	"errors"
	"fmt"
	"math/big"
)

var (
	ratZero = big.NewRat(0, 1)
	ratOne  = big.NewRat(1, 1)
)

const maxIters = 100000

// candidate is an internal extremum candidate with a refinable parameter
// interval and a certified value enclosure.
type candidate struct {
	kind    int // 0: t=0, 1: t=1, 2: interior root
	rootIdx int
	tIv     Iv       // for kind 2
	exact   *big.Rat // for endpoints
	valIv   Iv
	sIdx    int      // assigned value-polynomial root interval, -1 if none
	exactS  *big.Rat // if set: value is exactly this rational (gcd-certified)
	group   int
}

// polyHasRootIn reports whether the square-free polynomial g has a root in
// the isolating interval iv.
func polyHasRootIn(g Poly, iv Iv) bool {
	if g.Deg() < 1 {
		return false
	}
	if iv.IsPoint() {
		return g.Eval(iv.Lo).Sign() == 0
	}
	return NewSturm(g).CountOpen(iv.Lo, iv.Hi) == 1
}

// valueEnclosure bounds N1/D1 over the parameter interval t. It fails if the
// denominator interval still contains 0 (then t must be refined further).
func valueEnclosure(N1, D1 Poly, t Iv) (Iv, bool) {
	nIv := evalPolyIv(N1, t)
	dIv := evalPolyIv(D1, t)
	if dIv.ContainsZero() {
		return Iv{}, false
	}
	return divIv(nIv, dIv), true
}

// Analyze computes the global extrema of the rational Bezier curve with
// control values P (degree len(P)-1, must be 1..5) and positive weights W on
// [0,1], and returns a certificate in which the extreme values are enclosed
// in rational intervals of width at most eps. All arithmetic is exact.
func Analyze(P, W []*big.Rat, eps *big.Rat) (*Cert, error) {
	if len(P) < 2 || len(P) > 6 {
		return nil, fmt.Errorf("need 2..6 control values (degree 1..5), got %d", len(P))
	}
	if len(W) != len(P) {
		return nil, fmt.Errorf("got %d weights for %d control values", len(W), len(P))
	}
	for i, w := range W {
		if w == nil || w.Sign() <= 0 {
			return nil, fmt.Errorf("weight #%d must be a positive rational", i)
		}
	}
	if eps == nil || eps.Sign() <= 0 {
		return nil, errors.New("eps must be a positive rational")
	}

	N, D := buildND(P, W)
	g := GCD(N, D)
	N1, _ := QuoRem(N, g)
	D1, _ := QuoRem(D, g)
	// Derivative numerator: R'(t) = M(t) / D1(t)^2.
	M := Sub(Mul(N1.Deriv(), D1), Mul(N1, D1.Deriv()))

	cert := &Cert{P: P, W: W, Eps: eps}

	if M.IsZero() {
		// Constant curve: N1 = c*D1.
		cv := new(big.Rat).Quo(N1.Coeff(0), D1.Coeff(0))
		cert.Constant = true
		cert.ConstValue = cv
		cert.MaxLo, cert.MaxHi = cv, cv
		cert.MinLo, cert.MinHi = cv, cv
		return cert, nil
	}

	Msf := SquareFree(M)
	stM := NewSturm(Msf)
	cert.MPrim = intsToStrings(M.ToPrimitive())
	cert.Msf = polyToStrings(Msf)

	// All interior stationary points: distinct real roots of M in (0,1).
	roots := IsolateUnit(Msf)
	for _, r := range roots {
		cert.Roots = append(cert.Roots, IvCert{Lo: r.Lo, Hi: r.Hi})
	}

	// Monotonicity of R on each open segment between consecutive critical
	// points (endpoints 0,1 and the isolating intervals). The denominator is
	// positive on [0,1], so sign(R') = sign(M).
	bounds := []*big.Rat{ratZero}
	for _, r := range roots {
		bounds = append(bounds, r.Lo, r.Hi)
	}
	bounds = append(bounds, ratOne)
	for i := 0; i+1 < len(bounds); i += 2 {
		sample := avg(bounds[i], bounds[i+1])
		sign := M.Eval(sample).Sign()
		cert.Segments = append(cert.Segments, SegCert{Sample: sample, Sign: sign})
	}

	// Candidates: the two endpoints and every interior stationary point.
	var cands []*candidate
	mkEndpoint := func(kind int, t *big.Rat) *candidate {
		v := new(big.Rat).Quo(N1.Eval(t), D1.Eval(t))
		return &candidate{kind: kind, exact: v, valIv: Iv{new(big.Rat).Set(v), new(big.Rat).Set(v)}, sIdx: -1}
	}
	cands = append(cands, mkEndpoint(0, ratZero), mkEndpoint(1, ratOne))
	for i, r := range roots {
		cands = append(cands, &candidate{kind: 2, rootIdx: i, tIv: r, sIdx: -1})
	}

	ensureVal := func(c *candidate) error {
		if c.exactS != nil {
			c.valIv = Iv{new(big.Rat).Set(c.exactS), new(big.Rat).Set(c.exactS)}
			return nil
		}
		for it := 0; ; it++ {
			if it > maxIters {
				return errors.New("value enclosure did not converge")
			}
			v, ok := valueEnclosure(N1, D1, c.tIv)
			if ok {
				c.valIv = v
				return nil
			}
			c.tIv = RefineRoot(Msf, stM, c.tIv)
		}
	}
	refineCand := func(c *candidate) error {
		if c.kind != 2 {
			return nil
		}
		c.tIv = RefineRoot(Msf, stM, c.tIv)
		return ensureVal(c)
	}
	for _, c := range cands {
		if c.kind == 2 {
			if err := ensureVal(c); err != nil {
				return nil, err
			}
		}
	}

	// Value polynomial S(y): its roots include R(alpha) for every stationary
	// point alpha. Assigning each stationary point to an isolating interval
	// of S decides exactly which stationary values are equal (ties).
	var sIvs []Iv
	if len(roots) >= 2 {
		S := valuePoly(Msf, N1, D1)
		cert.S = polyToStrings(S)
		Ssf := SquareFree(S)
		cb := CauchyBound(Ssf)
		// The value-polynomial intervals stay FIXED: every stationary value
		// lies strictly inside its interval, so shrinking value enclosures
		// eventually fit (refining the intervals here would race the value
		// enclosures and could prevent convergence).
		sIvs = IsolateRoots(Ssf, new(big.Rat).Neg(cb), cb)
		contained := func(v Iv) int {
			for j, sv := range sIvs {
				if v.Lo.Cmp(sv.Lo) >= 0 && v.Hi.Cmp(sv.Hi) <= 0 {
					return j
				}
			}
			return -1
		}
		for _, c := range cands {
			if c.kind != 2 {
				continue
			}
			// A point isolating interval of S is an exactly known rational
			// root s. R(alpha) = s holds iff gcd(Msf, N1 - s*D1) vanishes at
			// alpha; decide this exactly instead of refining forever.
			for j, sv := range sIvs {
				if !sv.IsPoint() {
					continue
				}
				gv := GCD(Msf, Sub(N1, Scale(D1, sv.Lo)))
				if gv.Deg() >= 1 && polyHasRootIn(gv, roots[c.rootIdx]) {
					c.sIdx = j
					c.exactS = sv.Lo
					c.valIv = Iv{new(big.Rat).Set(sv.Lo), new(big.Rat).Set(sv.Lo)}
					break
				}
			}
			if c.exactS != nil {
				continue
			}
			for it := 0; ; it++ {
				if it > maxIters {
					return nil, errors.New("value-polynomial assignment did not converge")
				}
				if idx := contained(c.valIv); idx >= 0 {
					c.sIdx = idx
					break
				}
				if err := refineCand(c); err != nil {
					return nil, err
				}
			}
		}
	}

	// Group candidates whose values are exactly equal.
	parent := make([]int, len(cands))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) { parent[find(a)] = find(b) }

	// endpoint vs endpoint: exact rational comparison
	if cands[0].exact.Cmp(cands[1].exact) == 0 {
		union(0, 1)
	}
	// interior vs interior: same isolating interval of the value polynomial
	if len(roots) >= 2 {
		for i := 2; i < len(cands); i++ {
			for j := i + 1; j < len(cands); j++ {
				if cands[i].sIdx == cands[j].sIdx {
					union(i, j)
				}
			}
		}
	}
	// endpoint vs interior: R(alpha) = v  iff  gcd(Msf, N1 - v*D1) has alpha as a root
	for e := 0; e <= 1; e++ {
		v := cands[e].exact
		gv := GCD(Msf, Sub(N1, Scale(D1, v)))
		if gv.Deg() < 1 {
			continue
		}
		stG := NewSturm(gv)
		for i := 2; i < len(cands); i++ {
			riv := roots[cands[i].rootIdx]
			hit := false
			if riv.IsPoint() {
				hit = gv.Eval(riv.Lo).Sign() == 0
			} else {
				hit = stG.CountOpen(riv.Lo, riv.Hi) == 1
			}
			if hit {
				union(e, i)
			}
		}
	}

	gid := map[int]int{}
	for i := range cands {
		r := find(i)
		if _, ok := gid[r]; !ok {
			gid[r] = len(gid)
		}
		cands[i].group = gid[r]
	}

	// Separate the value enclosures of distinct groups: exact inequality proofs.
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[i].group == cands[j].group {
				continue
			}
			for it := 0; overlap(cands[i].valIv, cands[j].valIv); it++ {
				if it > maxIters {
					return nil, errors.New("group separation did not converge")
				}
				ri := cands[i].kind == 2 && cands[i].exactS == nil
				rj := cands[j].kind == 2 && cands[j].exactS == nil
				if !ri && !rj {
					return nil, errors.New("internal: equal exact values in distinct groups")
				}
				if ri {
					if err := refineCand(cands[i]); err != nil {
						return nil, err
					}
				}
				if rj {
					if err := refineCand(cands[j]); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	// Min/max groups by the now pairwise disjoint group enclosures.
	loOf := map[int]*big.Rat{}
	for _, c := range cands {
		if _, ok := loOf[c.group]; !ok {
			loOf[c.group] = c.valIv.Lo
		}
	}
	minG, maxG := -1, -1
	for g, lo := range loOf {
		if minG < 0 || lo.Cmp(loOf[minG]) < 0 {
			minG = g
		}
		if maxG < 0 || lo.Cmp(loOf[maxG]) > 0 {
			maxG = g
		}
	}

	// Certified enclosures of the extreme values with width <= eps.
	emit := func(g int) (*big.Rat, *big.Rat, error) {
		var best *candidate
		for _, c := range cands {
			if c.group != g {
				continue
			}
			if c.kind != 2 || c.exactS != nil { // exact rational value, width 0
				best = c
				break
			}
			if best == nil {
				best = c
			}
		}
		if best.kind != 2 {
			return new(big.Rat).Set(best.exact), new(big.Rat).Set(best.exact), nil
		}
		if best.exactS != nil {
			return new(big.Rat).Set(best.exactS), new(big.Rat).Set(best.exactS), nil
		}
		for it := 0; best.valIv.Width().Cmp(eps) > 0; it++ {
			if it > maxIters {
				return nil, nil, errors.New("extremum enclosure did not reach eps")
			}
			if err := refineCand(best); err != nil {
				return nil, nil, err
			}
		}
		return new(big.Rat).Set(best.valIv.Lo), new(big.Rat).Set(best.valIv.Hi), nil
	}
	maxLo, maxHi, err := emit(maxG)
	if err != nil {
		return nil, err
	}
	minLo, minHi, err := emit(minG)
	if err != nil {
		return nil, err
	}
	cert.MaxGroup, cert.MinGroup = maxG, minG
	cert.MaxLo, cert.MaxHi, cert.MinLo, cert.MinHi = maxLo, maxHi, minLo, minHi

	// Tighten the parameter intervals of all extremum-attaining stationary
	// points to width <= eps as well (the value enclosures already satisfy
	// this; tied extrema keep one certified interval per parameter).
	for _, c := range cands {
		if c.kind != 2 || (c.group != maxG && c.group != minG) {
			continue
		}
		for it := 0; c.tIv.Width().Cmp(eps) > 0; it++ {
			if it > maxIters {
				return nil, errors.New("parameter interval did not reach eps")
			}
			if err := refineCand(c); err != nil {
				return nil, err
			}
		}
	}

	// Final pass: re-establish containment of every root candidate's value
	// enclosure inside its (refined) value-polynomial interval.
	if len(roots) >= 2 {
		for _, c := range cands {
			if c.kind != 2 {
				continue
			}
			sv := sIvs[c.sIdx]
			for it := 0; !(c.valIv.Lo.Cmp(sv.Lo) >= 0 && c.valIv.Hi.Cmp(sv.Hi) <= 0); it++ {
				if it > maxIters {
					return nil, errors.New("value-polynomial containment did not converge")
				}
				if err := refineCand(c); err != nil {
					return nil, err
				}
			}
		}
	}

	for _, c := range cands {
		cc := CandCert{
			VLo:   new(big.Rat).Set(c.valIv.Lo),
			VHi:   new(big.Rat).Set(c.valIv.Hi),
			Group: c.group,
		}
		if c.exactS != nil {
			cc.VExact = new(big.Rat).Set(c.exactS)
		}
		switch c.kind {
		case 0:
			cc.Kind = KindT0
		case 1:
			cc.Kind = KindT1
		default:
			cc.Kind = KindRoot
			cc.RootIndex = c.rootIdx
			cc.TL = new(big.Rat).Set(c.tIv.Lo)
			cc.TH = new(big.Rat).Set(c.tIv.Hi)
			if c.sIdx >= 0 {
				cc.SL = new(big.Rat).Set(sIvs[c.sIdx].Lo)
				cc.SH = new(big.Rat).Set(sIvs[c.sIdx].Hi)
			}
		}
		cert.Candidates = append(cert.Candidates, cc)
	}
	return cert, nil
}
