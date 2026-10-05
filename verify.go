package main

import (
	"fmt"
	"math/big"
	"sort"
)

type SampleMismatch struct {
	Point            map[string]string `json:"point"`
	ProjectedHolds   bool              `json:"projected_holds"`
	OriginalFeasible bool              `json:"original_feasible"`
}

type VerifyReport struct {
	OriginalFeasible   bool             `json:"original_feasible"`
	ProjectedFeasible  bool             `json:"projected_feasible"`
	FeasibilityAgree   bool             `json:"feasibility_agree"`
	OriginalUnbounded  *bool            `json:"original_unbounded,omitempty"`
	ProjectedUnbounded *bool            `json:"projected_unbounded,omitempty"`
	SamplesChecked     int              `json:"samples_checked"`
	Mismatches         []SampleMismatch `json:"mismatches,omitempty"`
	ImplicitEqualities []string         `json:"implicit_equalities,omitempty"`
}

func splitStrict(ineqs []*Ineq) (leA [][]*big.Rat, leB []*big.Rat, ltA [][]*big.Rat, ltB []*big.Rat) {
	for _, in := range ineqs {
		if in.Strict {
			ltA = append(ltA, in.Coeffs)
			ltB = append(ltB, in.RHS)
		} else {
			leA = append(leA, in.Coeffs)
			leB = append(leB, in.RHS)
		}
	}
	return
}

// compact restricts constraints to the remaining variable indices.
func compact(ineqs []*Ineq, remIdx []int) []*Ineq {
	out := make([]*Ineq, len(ineqs))
	for i, in := range ineqs {
		coeffs := make([]*big.Rat, len(remIdx))
		for j, vi := range remIdx {
			coeffs[j] = in.Coeffs[vi]
		}
		out[i] = &Ineq{Coeffs: coeffs, RHS: in.RHS, Strict: in.Strict, Prov: in.Prov}
	}
	return out
}

func verify(original []*Ineq, nOrig int, projected []*Ineq, remIdx []int, contradiction bool, samples int, remNames []string) *VerifyReport {
	rep := &VerifyReport{}
	leA, leB, ltA, ltB := splitStrict(original)
	rep.OriginalFeasible, _ = feasibleStrict(leA, leB, ltA, ltB, nOrig)
	proj := compact(projected, remIdx)
	if contradiction {
		rep.ProjectedFeasible = false
	} else {
		pleA, pleB, pltA, pltB := splitStrict(proj)
		rep.ProjectedFeasible, _ = feasibleStrict(pleA, pleB, pltA, pltB, len(remIdx))
	}
	rep.FeasibilityAgree = rep.OriginalFeasible == rep.ProjectedFeasible
	if rep.OriginalFeasible {
		u := isUnbounded(leA, ltA, nOrig)
		rep.OriginalUnbounded = &u
	}
	if rep.ProjectedFeasible && !contradiction {
		pleA, _, pltA, _ := splitStrict(proj)
		u := isUnbounded(pleA, pltA, len(remIdx))
		rep.ProjectedUnbounded = &u
	}
	if rep.FeasibilityAgree && !contradiction {
		rep.SamplesChecked, rep.Mismatches = sampleCheck(original, nOrig, proj, remIdx, samples, remNames)
	}
	if !contradiction {
		rep.ImplicitEqualities = implicitEqualities(proj)
	}
	return rep
}

func evalAll(ineqs []*Ineq, point []*big.Rat) bool {
	for _, in := range ineqs {
		s := new(big.Rat)
		for j, c := range in.Coeffs {
			s.Add(s, new(big.Rat).Mul(c, point[j]))
		}
		cmp := s.Cmp(in.RHS)
		if in.Strict {
			if cmp >= 0 {
				return false
			}
		} else if cmp > 0 {
			return false
		}
	}
	return true
}

// sampleCheck enumerates small rational points in the remaining-variable
// space and compares the projected constraints against an independent
// feasibility query on the original system with remaining variables fixed.
func sampleCheck(original []*Ineq, nOrig int, proj []*Ineq, remIdx []int, budget int, remNames []string) (int, []SampleMismatch) {
	points := gridPoints(len(remIdx), budget)
	checked := 0
	var mismatches []SampleMismatch
	for _, p := range points {
		satProj := evalAll(proj, p)
		leA, leB, ltA, ltB := splitStrict(original)
		for i, vi := range remIdx {
			e := unitVector(nOrig, vi)
			leA = append(leA, e)
			leB = append(leB, p[i])
			leA = append(leA, negCoeffs(e))
			leB = append(leB, new(big.Rat).Neg(p[i]))
		}
		satOrig, _ := feasibleStrict(leA, leB, ltA, ltB, nOrig)
		checked++
		if satProj != satOrig {
			pt := map[string]string{}
			for i, name := range remNames {
				pt[name] = p[i].RatString()
			}
			mismatches = append(mismatches, SampleMismatch{
				Point:            pt,
				ProjectedHolds:   satProj,
				OriginalFeasible: satOrig,
			})
		}
	}
	return checked, mismatches
}

var gridValues = []*big.Rat{
	big.NewRat(-2, 1), big.NewRat(-1, 1), big.NewRat(-1, 2), big.NewRat(0, 1),
	big.NewRat(1, 2), big.NewRat(1, 1), big.NewRat(2, 1),
}

func gridPoints(k, budget int) [][]*big.Rat {
	if k == 0 {
		return [][]*big.Rat{{}}
	}
	total := 1
	for i := 0; i < k && total <= budget; i++ {
		total *= len(gridValues)
	}
	if total <= budget {
		var pts [][]*big.Rat
		idx := make([]int, k)
		for {
			p := make([]*big.Rat, k)
			for j := range p {
				p[j] = gridValues[idx[j]]
			}
			pts = append(pts, p)
			i := k - 1
			for ; i >= 0; i-- {
				idx[i]++
				if idx[i] < len(gridValues) {
					break
				}
				idx[i] = 0
			}
			if i < 0 {
				break
			}
		}
		return pts
	}
	var pts [][]*big.Rat
	state := uint64(0x9e3779b97f4a7c15)
	for len(pts) < budget {
		p := make([]*big.Rat, k)
		for j := range p {
			state = state*6364136223846793005 + 1442695040888963407
			p[j] = gridValues[(state>>33)%uint64(len(gridValues))]
		}
		pts = append(pts, p)
	}
	return pts
}

// implicitEqualities detects pairs a.x <= r and -a.x <= -r in the output,
// which together force the equality a.x = r.
func implicitEqualities(ineqs []*Ineq) []string {
	groups := map[string][]*Ineq{}
	var keys []string
	for _, in := range ineqs {
		k := coeffKey(in.Coeffs)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], in)
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if seen[k] {
			continue
		}
		g := groups[k]
		nk := coeffKey(negCoeffs(g[0].Coeffs))
		seen[k] = true
		seen[nk] = true
		g2, ok := groups[nk]
		if !ok {
			continue
		}
		s1 := strongestOf(g)
		s2 := strongestOf(g2)
		sum := new(big.Rat).Add(s1.RHS, s2.RHS)
		if sum.Sign() == 0 && !s1.Strict && !s2.Strict {
			out = append(out, fmt.Sprintf("(%s) . x = %s", coeffListString(s1.Coeffs), s1.RHS.RatString()))
		}
	}
	sort.Strings(out)
	return out
}

func strongestOf(group []*Ineq) *Ineq {
	best := group[0]
	for _, in := range group[1:] {
		if dominates(in, best) {
			best = in
		}
	}
	return best
}

func coeffListString(coeffs []*big.Rat) string {
	parts := make([]string, len(coeffs))
	for i, c := range coeffs {
		parts[i] = c.RatString()
	}
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += ","
		}
		s += p
	}
	return s
}
