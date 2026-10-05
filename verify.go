package main

import (
	"fmt"
	"math/big"
)

// verifyCert independently checks a certificate against the curve data.
// It recomputes every claim from first principles (exact rational
// arithmetic only) and returns an error describing the first failure.
func verifyCert(cert *Cert) error {
	c, err := cert.curve()
	if err != nil {
		return err
	}
	if len(c.Values) < 2 || len(c.Values) > 6 {
		return fmt.Errorf("degree must be 1..5")
	}
	for _, w := range c.Weights {
		if w.Sign() <= 0 {
			return fmt.Errorf("weights must be positive")
		}
	}
	eps, err := cert.epsilon()
	if err != nil {
		return err
	}

	P, _, D := c.derivNumerator()

	// (1) derivative numerator matches the claimed integer polynomial
	want := polyStrings(clearDenoms(P))
	if !strEq(want, cert.DerivNum) {
		return fmt.Errorf("derivative numerator mismatch: got %v, want %v", cert.DerivNum, want)
	}

	// (2) denominator strictly positive on [0,1]:
	// D(0) = w_0 > 0 and D has no root in (0,1].
	if D.eval(ratI(0)).Sign() <= 0 {
		return fmt.Errorf("denominator not positive at t=0")
	}
	dSF := squareFreeP(D)
	if dSF.Deg() >= 1 {
		dseq := sturmSeq(dSF)
		if n := countRoots(dseq, ratI(0), ratI(1)); n != 0 {
			return fmt.Errorf("denominator has %d roots in (0,1]", n)
		}
	}

	// (3) constant curve case
	if P.Zero() {
		if !cert.Constant {
			return fmt.Errorf("curve is constant but certificate says otherwise")
		}
		if len(cert.Stats) != 0 {
			return fmt.Errorf("constant curve must have no stationary points")
		}
		v := c.eval(ratI(0))
		for _, ex := range []ExtremumJSON{cert.Max, cert.Min} {
			if ex.ValueL != v.RatString() || ex.ValueR != v.RatString() {
				return fmt.Errorf("constant curve extremum value must be exactly %s", v)
			}
			if len(ex.Params) != 1 || ex.Params[0].L != "0" || ex.Params[0].R != "1" {
				return fmt.Errorf("constant curve extremum must cover all of [0,1]")
			}
		}
		return nil
	}
	if cert.Constant {
		return fmt.Errorf("certificate claims constant but derivative numerator is nonzero")
	}

	Q := squareFreeP(P)
	qseq := sturmSeq(Q)

	// (4) root count: number of claimed stationary points must equal the
	// Sturm count of distinct roots in the open interval (0,1)
	total := countRoots(qseq, ratI(0), ratI(1))
	if Q.eval(ratI(1)).Sign() == 0 {
		total--
	}
	if len(cert.Stats) != total {
		return fmt.Errorf("root count mismatch: certificate has %d, Sturm count is %d",
			len(cert.Stats), total)
	}

	// (5) each claimed isolating interval is valid; intervals disjoint & ordered
	stats := make([]Stationary, len(cert.Stats))
	prevR := ratI(0)
	for i, sj := range cert.Stats {
		l, ok1 := parseRat(sj.L)
		r, ok2 := parseRat(sj.R)
		if !ok1 || !ok2 {
			return fmt.Errorf("stationary %d: bad interval", i)
		}
		if l.Sign() <= 0 || r.Cmp(ratI(1)) >= 0 || l.Cmp(r) > 0 {
			return fmt.Errorf("stationary %d: interval must satisfy 0 < l <= r < 1", i)
		}
		if l.Cmp(prevR) < 0 {
			return fmt.Errorf("stationary %d: intervals overlap or are unordered", i)
		}
		prevR = r
		if new(big.Rat).Sub(r, l).Cmp(eps) > 0 {
			return fmt.Errorf("stationary %d: interval width exceeds epsilon", i)
		}
		iv := Interval{l, r}
		if l.Cmp(r) == 0 {
			if Q.eval(l).Sign() != 0 {
				return fmt.Errorf("stationary %d: exact point is not a root", i)
			}
		} else {
			if Q.eval(l).Sign() == 0 || Q.eval(r).Sign() == 0 {
				return fmt.Errorf("stationary %d: endpoint is a root; use exact form", i)
			}
			if countRoots(qseq, l, r) != 1 {
				return fmt.Errorf("stationary %d: interval does not isolate exactly one root", i)
			}
		}
		// (6) multiplicity, verified exactly via gcd(Q, P^(k))
		m := multiplicity(P, Q, iv)
		if m != sj.Multiplicity {
			return fmt.Errorf("stationary %d: multiplicity %d claimed, %d verified",
				i, sj.Multiplicity, m)
		}
		stats[i] = Stationary{Iv: iv, Mult: m}
	}

	// (7) monotonicity: independently sampled signs between consecutive roots
	regions := make([]int, len(stats)+1)
	for i := 0; i <= len(stats); i++ {
		var s *big.Rat
		if len(stats) == 0 {
			s = ratI(1)
			s.Quo(s, ratI(2))
		} else if i == 0 {
			s = avg(ratI(0), stats[0].Iv.L)
		} else if i == len(stats) {
			s = avg(stats[len(stats)-1].Iv.R, ratI(1))
		} else {
			s = avg(stats[i-1].Iv.R, stats[i].Iv.L)
		}
		regions[i] = P.eval(s).Sign()
		if regions[i] == 0 {
			return fmt.Errorf("internal: zero sign sample in region %d", i)
		}
	}
	for i := range stats {
		if signWord(regions[i]) != cert.Stats[i].Left ||
			signWord(regions[i+1]) != cert.Stats[i].Right {
			return fmt.Errorf("stationary %d: monotonicity labels wrong", i)
		}
		role := "none"
		m := stats[i].Mult
		switch {
		case m%2 == 1 && regions[i] > 0 && regions[i+1] < 0:
			role = "max"
		case m%2 == 1 && regions[i] < 0 && regions[i+1] > 0:
			role = "min"
		}
		if role != cert.Stats[i].Role {
			return fmt.Errorf("stationary %d: role %q claimed, %q verified",
				i, cert.Stats[i].Role, role)
		}
	}

	// (8) recompute candidates, exact tie groups and global extrema from the
	// certificate's own intervals, then compare with the claimed extrema.
	sol := &Solution{Curve: c, Eps: eps, P: P, Q: Q, Stats: stats, Regions: regions}
	if _, err := fillExtrema(sol); err != nil {
		return err
	}
	sol.qseq = qseq
	if err := checkExtremum("max", cert.Max, sol, eps); err != nil {
		return err
	}
	if err := checkExtremum("min", cert.Min, sol, eps); err != nil {
		return err
	}
	return nil
}

// checkExtremum verifies one claimed global extremum against the recomputed one.
func checkExtremum(name string, ex ExtremumJSON, sol *Solution, eps *big.Rat) error {
	val, params := sol.MaxVal, sol.MaxParams
	if name == "min" {
		val, params = sol.MinVal, sol.MinParams
	}
	vl, ok1 := parseRat(ex.ValueL)
	vr, ok2 := parseRat(ex.ValueR)
	if !ok1 || !ok2 || vl.Cmp(vr) > 0 {
		return fmt.Errorf("global %s: bad value interval", name)
	}
	// enclosure reliability: must contain the certified value enclosure
	if vl.Cmp(val.L) > 0 || vr.Cmp(val.R) < 0 {
		return fmt.Errorf("global %s: claimed value interval [%s,%s] does not enclose certified [%s,%s]",
			name, vl, vr, val.L, val.R)
	}
	if new(big.Rat).Sub(vr, vl).Cmp(eps) > 0 {
		return fmt.Errorf("global %s: value interval width exceeds epsilon", name)
	}
	// parameter sets must match exactly (all tied points preserved)
	want := map[string]bool{}
	for _, p := range params {
		k, err := paramKey(p, sol.Q, sol.qseq)
		if err != nil {
			return fmt.Errorf("global %s: internal param error: %v", name, err)
		}
		want[k] = true
	}
	got := map[string]bool{}
	for _, pj := range ex.Params {
		l, ok1 := parseRat(pj.L)
		r, ok2 := parseRat(pj.R)
		if !ok1 || !ok2 || l.Cmp(r) > 0 {
			return fmt.Errorf("global %s: bad param interval", name)
		}
		if new(big.Rat).Sub(r, l).Cmp(eps) > 0 {
			return fmt.Errorf("global %s: param interval width exceeds epsilon", name)
		}
		k, err := paramKey(Interval{l, r}, sol.Q, sol.qseq)
		if err != nil {
			return fmt.Errorf("global %s: %v", name, err)
		}
		got[k] = true
	}
	if len(got) != len(ex.Params) {
		return fmt.Errorf("global %s: duplicate param intervals", name)
	}
	for k := range want {
		if !got[k] {
			return fmt.Errorf("global %s: missing tied parameter %s", name, k)
		}
	}
	for k := range got {
		if !want[k] {
			return fmt.Errorf("global %s: spurious parameter %s", name, k)
		}
	}
	return nil
}

// paramKey classifies a parameter interval as endpoint 0, endpoint 1, or
// the k-th interior root of Q. Interior intervals must themselves isolate
// exactly one root; otherwise an error is returned.
func paramKey(p Interval, Q Poly, qseq []Poly) (string, error) {
	if p.L.Cmp(p.R) == 0 {
		if p.L.Sign() == 0 {
			return "t=0", nil
		}
		if p.L.Cmp(ratI(1)) == 0 {
			return "t=1", nil
		}
	}
	if p.L.Sign() <= 0 || p.R.Cmp(ratI(1)) >= 0 {
		return "", fmt.Errorf("param interval [%s,%s] outside (0,1)", p.L, p.R)
	}
	if p.L.Cmp(p.R) == 0 {
		if Q.eval(p.L).Sign() != 0 {
			return "", fmt.Errorf("exact param %s is not a stationary point", p.L)
		}
	} else {
		if Q.eval(p.L).Sign() == 0 || Q.eval(p.R).Sign() == 0 {
			return "", fmt.Errorf("param interval endpoint is a root; use exact form")
		}
		if countRoots(qseq, p.L, p.R) != 1 {
			return "", fmt.Errorf("param interval [%s,%s] does not isolate exactly one root", p.L, p.R)
		}
	}
	k := countRoots(qseq, ratI(0), p.L)
	if p.L.Cmp(p.R) == 0 {
		k-- // the root sits exactly at L; (0,L] counted it
	}
	return fmt.Sprintf("root#%d", k), nil
}

func strEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
