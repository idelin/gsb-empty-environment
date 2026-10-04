package domain

import (
	"fmt"
	"math/big"
)

// Result is the outcome of a domain analysis: the (provably defined)
// piecewise domain plus human-readable notes about the parts that could
// not be decided exactly and were therefore excluded.
type Result struct {
	Domain      Domain
	Unsupported []string
}

func (r Result) String() string {
	s := "domain: " + r.Domain.String()
	if len(r.Unsupported) == 0 {
		return s + "\nunsupported: (none)"
	}
	s += "\nunsupported:"
	for _, n := range r.Unsupported {
		s += "\n  - " + n
	}
	return s
}

// Analyze computes the exact domain of e for the variable ranging over iv.
// Only exactly decidable real branches are kept; anything that cannot be
// resolved with exact rational arithmetic is reported in Unsupported.
func Analyze(e *Expr, iv Interval) Result {
	dom, notes := domainOf(e, Domain{Intervals: []Interval{iv}})
	return Result{Domain: dom, Unsupported: dedupe(notes)}
}

// AnalyzeString is a convenience wrapper: it parses the expression and the
// interval endpoints (e.g. "-3/2", "2.5", "4").
func AnalyzeString(src, varName, lo, hi string, loOpen, hiOpen bool) (Result, error) {
	e, err := Parse(src, varName)
	if err != nil {
		return Result{}, err
	}
	loR, ok1 := new(big.Rat).SetString(lo)
	hiR, ok2 := new(big.Rat).SetString(hi)
	if !ok1 || !ok2 {
		return Result{}, fmt.Errorf("invalid interval endpoint")
	}
	if loR.Cmp(hiR) > 0 {
		return Result{}, fmt.Errorf("empty interval: lo > hi")
	}
	iv := Interval{Lo: Endpoint{loR, loOpen}, Hi: Endpoint{hiR, hiOpen}}
	return Analyze(e, iv), nil
}

type requirement int

const (
	reqAll requirement = iota
	reqNE0             // base != 0
	reqGE0             // base >= 0
	reqGT0             // base > 0
)

// domainOf computes the subset of dom on which e is defined, plus notes
// about parts that had to be excluded because they were undecidable.
func domainOf(e *Expr, dom Domain) (Domain, []string) {
	switch e.Kind {
	case KConst, KVar:
		return dom, nil
	case KNeg:
		return domainOf(e.A, dom)
	case KAdd, KSub, KMul:
		cur := dom
		var notes []string
		for _, c := range [2]*Expr{e.A, e.B} {
			d, un := domainOf(c, cur)
			cur = d
			notes = append(notes, un...)
		}
		return cur, notes
	case KDiv:
		cur, notes := domainOf(e.A, dom)
		d, un := domainOf(e.B, cur)
		cur = d
		notes = append(notes, un...)
		part, un2 := signPartition(e.B, cur)
		notes = append(notes, un2...)
		kept, un3 := keepRegions(e.B, part, reqNE0)
		return domainFromRegions(kept), append(notes, un3...)
	case KPow:
		cur, notes := domainOf(e.A, dom)
		p := e.Exp.Num().Int64()
		q := e.Exp.Denom().Int64()
		if p == 0 {
			return cur, notes // x^0 == 1 everywhere
		}
		req := reqAll
		switch {
		case q == 1:
			if p < 0 {
				req = reqNE0
			}
		case q%2 == 0:
			if p < 0 {
				req = reqGT0
			} else {
				req = reqGE0
			}
		default: // odd denominator: real branch exists for negative bases
			if p < 0 {
				req = reqNE0
			}
		}
		if req == reqAll {
			return cur, notes
		}
		part, un2 := signPartition(e.A, cur)
		notes = append(notes, un2...)
		kept, un3 := keepRegions(e.A, part, req)
		return domainFromRegions(kept), append(notes, un3...)
	}
	return dom, []string{fmt.Sprintf("unsupported expression %s", e)}
}

// keepRegions filters a sign partition by a requirement on the
// expression's sign. Regions that provably violate the requirement are
// dropped silently; regions that cannot be decided are dropped too but
// reported as unsupported.
func keepRegions(e *Expr, part []Region, req requirement) ([]Region, []string) {
	var kept []Region
	var notes []string
	rel := map[requirement]string{reqNE0: "!= 0", reqGE0: ">= 0", reqGT0: "> 0"}[req]
	for _, r := range part {
		keep, decidable := false, true
		switch req {
		case reqAll:
			keep = true
		case reqNE0:
			switch r.Sign {
			case SPos, SNeg:
				keep = true
			case SZero:
			default:
				decidable = false
			}
		case reqGE0:
			switch r.Sign {
			case SPos, SZero, SNonNeg:
				keep = true
			case SNeg:
			default:
				decidable = false
			}
		case reqGT0:
			switch r.Sign {
			case SPos:
				keep = true
			case SZero, SNeg, SNonPos:
			default:
				decidable = false
			}
		}
		switch {
		case keep:
			kept = append(kept, r)
		case !decidable:
			notes = append(notes, fmt.Sprintf("cannot prove %s %s on %s; that part is excluded from the reported domain", e, rel, r.Interval))
		}
	}
	return kept, notes
}

// domainFromRegions builds a domain from kept regions, merging adjacent
// intervals whose shared endpoint is covered by at least one side.
func domainFromRegions(rs []Region) Domain {
	out := make([]Interval, 0, len(rs))
	for _, r := range rs {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.Hi.Val.Cmp(r.Lo.Val) == 0 && (!last.Hi.Open || !r.Lo.Open) {
				last.Hi = r.Hi
				continue
			}
		}
		out = append(out, r.Interval)
	}
	return Domain{Intervals: out}
}

func dedupe(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := ss[:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
