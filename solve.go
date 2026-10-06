package main

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
)

// Term is one rational input: Num/Den with Den > 0.
type Term struct {
	Num *big.Int
	Den *big.Int
}

// Step records one accumulation step of the chosen permutation.
type Step struct {
	Index int      // original input index
	Input *big.Rat // the input term x
	Pre   *big.Rat // acc + x before rounding
	Post  *big.Rat // fixed-point value after half-even rounding to p digits
}

// Result is the outcome of a solve.
type Result struct {
	P         int
	Perm      []int    // permutation of original indices
	Steps     []Step   // one per input, in evaluation order
	Final     *big.Rat // final accumulator value
	Exact     *big.Rat // exact sum of all inputs
	Deviation *big.Rat // |Final - Exact|
}

func pow10(p int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(p)), nil)
}

// roundHalfEven returns the nearest integer to num/den (den > 0),
// breaking ties to the even neighbour.
func roundHalfEven(num, den *big.Int) *big.Int {
	a := new(big.Int).Abs(num)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(a, den, r) // a = q*den + r, 0 <= r < den
	twice := new(big.Int).Lsh(r, 1)
	cmp := twice.Cmp(den)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	if num.Sign() < 0 {
		q.Neg(q)
	}
	return q
}

// roundRatHalfEven rounds r to p decimal digits, half-even.
func roundRatHalfEven(r *big.Rat, p int) *big.Rat {
	scale := pow10(p)
	k := roundHalfEven(new(big.Int).Mul(r.Num(), scale), r.Denom())
	return new(big.Rat).SetFrac(k, scale)
}

func termRat(t Term) *big.Rat {
	return new(big.Rat).SetFrac(new(big.Int).Set(t.Num), new(big.Int).Set(t.Den))
}

func exactSum(terms []Term) *big.Rat {
	s := new(big.Rat)
	for _, t := range terms {
		s.Add(s, termRat(t))
	}
	return s
}

// roundStepInt advances the scaled accumulator acc (acc/scale is its value)
// by one term, returning the new scaled accumulator. Exact integer arithmetic.
func roundStepInt(acc *big.Int, t Term, scale *big.Int) *big.Int {
	// acc/scale + num/den = (acc*den + num*scale) / (den*scale)
	num := new(big.Int).Mul(acc, t.Den)
	num.Add(num, new(big.Int).Mul(t.Num, scale))
	return roundHalfEven(num, t.Den)
}

func lessPath(a, b []byte) bool { return bytes.Compare(a, b) < 0 }

// replay recomputes the step trace for a permutation with big.Rat arithmetic.
func replay(terms []Term, perm []int, p int) (*big.Rat, []Step) {
	acc := new(big.Rat)
	steps := make([]Step, 0, len(perm))
	for _, i := range perm {
		x := termRat(terms[i])
		pre := new(big.Rat).Add(acc, x)
		post := roundRatHalfEven(pre, p)
		steps = append(steps, Step{Index: i, Input: x, Pre: pre, Post: post})
		acc = post
	}
	return acc, steps
}

func buildResult(terms []Term, perm []int, p int) *Result {
	final, steps := replay(terms, perm, p)
	exact := exactSum(terms)
	dev := new(big.Rat).Sub(final, exact)
	dev.Abs(dev)
	permCopy := append([]int(nil), perm...)
	return &Result{P: p, Perm: permCopy, Steps: steps, Final: final, Exact: exact, Deviation: dev}
}

type dpState struct {
	acc  *big.Int // scaled by 10^p
	path []byte   // lexicographically smallest index path reaching (mask, acc)
}

// Solve finds the permutation of the n (1..12) inputs that minimises the
// absolute deviation of the step-by-step rounded accumulator from the exact
// sum. Ties are broken by lexicographic order of the original indices.
//
// It uses exact dynamic programming over subsets: for every (subset, rounded
// accumulator value) it keeps only the lexicographically smallest path, which
// is sufficient because the future evolution depends only on the accumulator.
func Solve(terms []Term, p int) (*Result, error) {
	n := len(terms)
	if n < 1 || n > 12 {
		return nil, fmt.Errorf("need between 1 and 12 inputs, got %d", n)
	}
	if p < 0 {
		return nil, fmt.Errorf("precision p must be >= 0, got %d", p)
	}
	scale := pow10(p)
	full := 1 << uint(n)
	states := make([]map[string]*dpState, full)
	states[0] = map[string]*dpState{"0": {acc: new(big.Int)}}
	for mask := 0; mask < full; mask++ {
		for _, s := range states[mask] {
			for i := 0; i < n; i++ {
				bit := 1 << uint(i)
				if mask&bit != 0 {
					continue
				}
				nm := mask | bit
				na := roundStepInt(s.acc, terms[i], scale)
				key := na.String()
				cand := make([]byte, len(s.path)+1)
				copy(cand, s.path)
				cand[len(s.path)] = byte(i)
				m := states[nm]
				if m == nil {
					m = map[string]*dpState{}
					states[nm] = m
				}
				if ex, ok := m[key]; !ok || lessPath(cand, ex.path) {
					m[key] = &dpState{acc: na, path: cand}
				}
			}
		}
	}
	exact := exactSum(terms)
	var bestDev *big.Rat
	var bestPath []byte
	for _, s := range states[full-1] {
		accRat := new(big.Rat).SetFrac(s.acc, scale)
		dev := new(big.Rat).Sub(accRat, exact)
		dev.Abs(dev)
		if bestDev == nil || dev.Cmp(bestDev) < 0 ||
			(dev.Cmp(bestDev) == 0 && lessPath(s.path, bestPath)) {
			bestDev = dev
			bestPath = s.path
		}
	}
	perm := make([]int, n)
	for i, b := range bestPath {
		perm[i] = int(b)
	}
	return buildResult(terms, perm, p), nil
}

// BruteForce is an independent verification: it enumerates all permutations
// recursively and simulates each one step by step with big.Rat arithmetic.
// Intended for n <= 8.
func BruteForce(terms []Term, p int) (*Result, error) {
	n := len(terms)
	if n < 1 || n > 8 {
		return nil, fmt.Errorf("brute force supports 1..8 inputs, got %d", n)
	}
	if p < 0 {
		return nil, fmt.Errorf("precision p must be >= 0, got %d", p)
	}
	exact := exactSum(terms)
	var bestDev *big.Rat
	var bestPerm []int
	perm := make([]int, 0, n)
	used := make([]bool, n)
	var rec func(acc *big.Rat)
	rec = func(acc *big.Rat) {
		if len(perm) == n {
			dev := new(big.Rat).Sub(acc, exact)
			dev.Abs(dev)
			if bestDev == nil || dev.Cmp(bestDev) < 0 {
				bestDev = dev
				bestPerm = append([]int(nil), perm...)
			}
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			perm = append(perm, i)
			rec(roundRatHalfEven(new(big.Rat).Add(acc, termRat(terms[i])), p))
			perm = perm[:len(perm)-1]
			used[i] = false
		}
	}
	rec(new(big.Rat))
	return buildResult(terms, bestPerm, p), nil
}

// parseTerm parses "num/den" or a plain integer.
func parseTerm(s string) (Term, error) {
	var t Term
	if i := strings.IndexByte(s, '/'); i >= 0 {
		num, ok1 := new(big.Int).SetString(strings.TrimSpace(s[:i]), 10)
		den, ok2 := new(big.Int).SetString(strings.TrimSpace(s[i+1:]), 10)
		if !ok1 || !ok2 {
			return t, fmt.Errorf("invalid rational %q", s)
		}
		if den.Sign() <= 0 {
			return t, fmt.Errorf("denominator must be a positive integer in %q", s)
		}
		t = Term{Num: num, Den: den}
	} else {
		num, ok := new(big.Int).SetString(strings.TrimSpace(s), 10)
		if !ok {
			return t, fmt.Errorf("invalid integer %q", s)
		}
		t = Term{Num: num, Den: big.NewInt(1)}
	}
	return t, nil
}

func parseTerms(args []string) ([]Term, error) {
	terms := make([]Term, len(args))
	for i, a := range args {
		t, err := parseTerm(a)
		if err != nil {
			return nil, err
		}
		terms[i] = t
	}
	return terms, nil
}

// formatFixed prints k/10^p with exactly p fractional digits.
func formatFixed(k *big.Int, p int) string {
	if p == 0 {
		return k.String()
	}
	neg := k.Sign() < 0
	a := new(big.Int).Abs(k)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(a, pow10(p), r)
	frac := r.String()
	for len(frac) < p {
		frac = "0" + frac
	}
	s := q.String() + "." + frac
	if neg {
		return "-" + s
	}
	return s
}

// formatRatFixed prints a rational known to be a multiple of 10^-p.
func formatRatFixed(r *big.Rat, p int) string {
	k := new(big.Int).Quo(new(big.Int).Mul(r.Num(), pow10(p)), r.Denom())
	return formatFixed(k, p)
}

// approxDecimal prints a short decimal approximation of a rational.
func approxDecimal(r *big.Rat) string {
	f := new(big.Float).SetPrec(256).SetRat(r)
	return f.Text('f', 10)
}
