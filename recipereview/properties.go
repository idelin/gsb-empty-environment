package recipereview

import (
	"bytes"
	"fmt"
	"sort"
)

func buildProperties(ordered []*Recipe, compat *CompatTable) []*property {
	props := make([]*property, 0)
	for _, r := range ordered {
		props = append(props, &property{
			kind:      propInjective,
			label:     fmt.Sprintf("injective:%d(%s/%s)", r.ID, r.Purpose, r.Version),
			r1:        r,
			r2:        r,
			symbolic:  proveInjective(r),
			enumerate: true,
		})
	}
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			r1, r2 := ordered[i], ordered[j]
			differentPurpose := r1.Purpose != r2.Purpose
			coexist := differentPurpose || compat.Allows(r1.ref(), r2.ref())
			if !coexist {
				continue
			}
			if differentPurpose {
				props = append(props, &property{
					kind:      propDistinct,
					label:     fmt.Sprintf("distinct:%d:%d", r1.ID, r2.ID),
					r1:        r1,
					r2:        r2,
					symbolic:  proveDistinct(r1, r2),
					enumerate: true,
				})
				if r1.ReadOnly != r2.ReadOnly {
					props = append(props, &property{
						kind:  propReadOnly,
						label: fmt.Sprintf("readonly:%d:%d", r1.ID, r2.ID),
						r1:    r1,
						r2:    r2,
						symbolic: ProofResult{
							Status: SymbolicInconclusive,
							Reason: "read-only acceptance requires enumeration",
						},
						enumerate: true,
					})
				}
			} else {
				props = append(props, &property{
					kind:      propDistinct,
					label:     fmt.Sprintf("compat:%d:%d", r1.ID, r2.ID),
					r1:        r1,
					r2:        r2,
					symbolic:  proveDistinct(r1, r2),
					enumerate: true,
				})
			}
		}
	}
	return props
}

type candidate struct {
	prop    *property
	a, b    Assignment
	witness *Witness
}

func runEnumeration(report *Report, ordered []*Recipe, props []*property,
	assignments map[*Recipe][]Assignment, budget *Budget) {
	maxTotal := 0
	for _, r := range ordered {
		m := 0
		for _, f := range r.Fields {
			m += f.MaxLen
		}
		if m > maxTotal {
			maxTotal = m
		}
	}

	bestByProperty := map[string]*Witness{}
	for total := 0; total <= 2*maxTotal; total++ {
		if len(bestByProperty) == len(props) {
			break
		}
		candidates := buildCandidates(props, assignments, total)
		sortCandidates(candidates)
		for _, cand := range candidates {
			if bestByProperty[cand.prop.label] != nil {
				continue
			}
			if !budget.charge(1) {
				markUnknown(report, props, "state budget exhausted while checking candidates")
				finalizeEnumeration(report, props, bestByProperty, budget)
				return
			}
			w, err := evaluateCandidate(cand)
			if err != nil {
				markUnknown(report, props, "enumeration error: "+err.Error())
				finalizeEnumeration(report, props, bestByProperty, budget)
				return
			}
			if w != nil {
				bestByProperty[cand.prop.label] = w
			}
		}
	}
	finalizeEnumeration(report, props, bestByProperty, budget)
}

func finalizeEnumeration(report *Report, props []*property, bestByProperty map[string]*Witness, budget *Budget) {
	index := map[string]*Verdict{}
	for _, v := range report.Verdicts {
		index[v.Property] = v
	}
	for _, p := range props {
		v := index[p.label]
		v.Enumerated = true
		best := bestByProperty[p.label]
		switch {
		case best != nil:
			v.Kind = VerdictConflict
			v.Witness = best
			v.Reason = "violating input pair found by full enumeration"
		case budget.Exhausted():
			v.Kind = VerdictUnknown
			if v.Reason == "" || v.Reason == p.symbolic.Reason {
				v.Reason = "state budget exhausted before property was settled"
			}
		default:
			v.Kind = VerdictSafe
			v.Reason = "full enumeration of the finite field domain found no violation"
		}
	}
}

func buildCandidates(props []*property, assignments map[*Recipe][]Assignment, total int) []candidate {
	out := make([]candidate, 0)
	for _, p := range props {
		list1 := assignments[p.r1]
		list2 := assignments[p.r2]
		if p.kind == propInjective {
			for i, a := range list1 {
				if assignmentTotalLen(p.r1, a) != total {
					continue
				}
				for j := i + 1; j < len(list1); j++ {
					b := list1[j]
					if assignmentTotalLen(p.r1, b) != total {
						continue
					}
					w := &Witness{
						Kind: WitnessCollision, FirstRecipeID: p.r1.ID, SecondRecipeID: p.r1.ID,
						First: a, Second: b, Pair: makePairKey(p.r1, a, p.r1, b),
					}
					out = append(out, candidate{prop: p, a: a, b: b, witness: w})
				}
			}
			continue
		}
		for _, a := range list1 {
			la := assignmentTotalLen(p.r1, a)
			for _, b := range list2 {
				if la+assignmentTotalLen(p.r2, b) != total {
					continue
				}
				kind := WitnessCollision
				if p.kind == propReadOnly {
					kind = WitnessReadOnly
				}
				w := &Witness{
					Kind: kind, FirstRecipeID: p.r1.ID, SecondRecipeID: p.r2.ID,
					First: a, Second: b, Pair: makePairKey(p.r1, a, p.r2, b),
				}
				out = append(out, candidate{prop: p, a: a, b: b, witness: w})
			}
		}
	}
	return out
}

func sortCandidates(cs []candidate) {
	sort.Slice(cs, func(i, j int) bool {
		return comparePairKey(cs[i].witness.Pair, cs[j].witness.Pair) < 0
	})
}

func evaluateCandidate(cand candidate) (*Witness, error) {
	p := cand.prop
	w := cand.witness
	switch p.kind {
	case propInjective, propDistinct:
		info1, err := p.r1.Encode(cand.a)
		if err != nil {
			return nil, err
		}
		info2, err := p.r2.Encode(cand.b)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(info1, info2) {
			w.FirstInfo, w.SecondInfo = info1, info2
			return w, nil
		}
		return nil, nil
	case propReadOnly:
		info, err := readonlyEncoding(p, cand.a, cand.b)
		if err != nil {
			return nil, err
		}
		if info != nil {
			w.FirstInfo = append([]byte(nil), info...)
			w.SecondInfo = append([]byte(nil), info...)
			return w, nil
		}
		return nil, nil
	}
	return nil, nil
}

// readonlyEncoding returns the shared bytes when a read-only side's encoding
// is accepted as a complete valid encoding by the other side and decodes to
// the claimed assignment.
func readonlyEncoding(p *property, a, b Assignment) ([]byte, error) {
	if p.r1.ReadOnly == p.r2.ReadOnly {
		return nil, nil
	}
	if p.r1.ReadOnly && !p.r2.ReadOnly {
		info, err := p.r1.Encode(a)
		if err != nil {
			return nil, err
		}
		if decoded, err := p.r2.Decode(info); err == nil && assignmentsEqual(p.r2, decoded, b) {
			return info, nil
		}
	}
	if p.r2.ReadOnly && !p.r1.ReadOnly {
		info, err := p.r2.Encode(b)
		if err != nil {
			return nil, err
		}
		if decoded, err := p.r1.Decode(info); err == nil && assignmentsEqual(p.r1, decoded, a) {
			return info, nil
		}
	}
	return nil, nil
}

func assignmentsEqual(r *Recipe, got, want Assignment) bool {
	for name := range r.Fields {
		if !bytes.Equal(got[name], want[name]) {
			return false
		}
	}
	return true
}
