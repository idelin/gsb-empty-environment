package admission

import (
	"fmt"
	"math/big"
)

// MaxRules is the largest number of deterministic admission rules that can be
// enumerated: 2^MaxDecisionPoints.
const MaxRules = 1 << MaxDecisionPoints

// CategoryBound is the exact worst-case deviation for one audit category.
type CategoryBound struct {
	Category string
	// Ideal is the ideal conditional share.
	Ideal *big.Rat
	// WorstConditionalDeviation is the largest |conditional share - ideal|
	// across all admissible rules. When no rule can complete a round it is
	// set to one, the trivial tight bound.
	WorstConditionalDeviation *big.Rat
	// WorstMassDeviation is the largest |unconditional category mass -
	// ideal * completion|-equivalent absolute departure of the unconditional
	// category mass from its ideal share.
	WorstMassDeviation *big.Rat
}

// Result is the complete exhaustive analysis.
type Result struct {
	// PolicyCount is the number of deterministic rules enumerated.
	PolicyCount int
	// CategoryBounds covers every successful audit category.
	CategoryBounds []CategoryBound
	// FailureBound is a tight upper bound on the probability that a round
	// cannot complete within the public retry limit.
	FailureBound *big.Rat
	// MinCompletion is the smallest completion probability observed; it is
	// the denominator actually available to a reviewer.
	MinCompletion *big.Rat

	// Witnesses are retained for local auditing only. They prove each bound
	// is attained by some public rule. They are never written into a
	// Certificate, which carries no manipulation recipe.
	DeviationWitness map[string]Policy
	FailureWitness   Policy

	records []string
}

// Enumerate evaluates every deterministic admission rule permitted by the
// model and returns exact worst-case bounds. The public decision count is
// capped at 14, so this is a complete enumeration, never a sample.
func (a *Analyzer) Enumerate() (*Result, error) {
	combos := 1
	for _, v := range a.vars {
		if !v.hostFixed {
			combos *= len(v.actions)
		}
	}
	if combos > MaxRules {
		return nil, fmt.Errorf("admission: %d admissible rules exceed enumeration cap %d", combos, MaxRules)
	}

	categories := a.categories()
	res := &Result{
		PolicyCount:      0,
		FailureBound:     new(big.Rat),
		MinCompletion:    big.NewRat(1, 1),
		DeviationWitness: map[string]Policy{},
	}
	bounds := map[string]*CategoryBound{}
	for _, cat := range categories {
		bounds[cat] = &CategoryBound{
			Category:                  cat,
			Ideal:                     new(big.Rat).Set(a.model.Ideal[cat]),
			WorstConditionalDeviation: new(big.Rat),
			WorstMassDeviation:        new(big.Rat),
		}
	}

	digits := make([]int, len(a.vars))
	for {
		policy := make(Policy, len(a.vars))
		for i, v := range a.vars {
			if v.hostFixed {
				policy[v.id] = v.hostAct
			} else {
				policy[v.id] = v.actions[digits[i]]
			}
		}
		if err := a.accumulate(res, bounds, policy, categories); err != nil {
			return nil, err
		}
		if !increment(digits, a.vars) {
			break
		}
	}

	for _, cat := range categories {
		res.CategoryBounds = append(res.CategoryBounds, *bounds[cat])
	}
	return res, nil
}

func increment(digits []int, vars []*conditionVar) bool {
	for i := range digits {
		v := vars[i]
		if v.hostFixed {
			continue
		}
		if digits[i]+1 < len(v.actions) {
			digits[i]++
			return true
		}
		digits[i] = 0
	}
	return false
}

func (a *Analyzer) accumulate(res *Result, bounds map[string]*CategoryBound, policy Policy, categories []string) error {
	dist, err := a.Evaluate(policy)
	if err != nil {
		return err
	}
	res.PolicyCount++
	res.records = append(res.records, a.recordLine(policy, dist))

	if dist.FailureMass.Cmp(res.FailureBound) > 0 {
		res.FailureBound.Set(dist.FailureMass)
		res.FailureWitness = clonePolicy(policy)
	}
	if dist.CompletionMass.Cmp(res.MinCompletion) < 0 {
		res.MinCompletion.Set(dist.CompletionMass)
	}

	for _, cat := range categories {
		b := bounds[cat]
		massDev := absRat(new(big.Rat).Sub(dist.SuccessMass[cat], b.Ideal))
		if massDev.Cmp(b.WorstMassDeviation) > 0 {
			b.WorstMassDeviation.Set(massDev)
		}
		var condDev *big.Rat
		if dist.ConditionalShare == nil {
			// No completed round: allocation is undefined, use the trivial
			// tight deviation bound of one.
			condDev = big.NewRat(1, 1)
		} else {
			condDev = absRat(new(big.Rat).Sub(dist.ConditionalShare[cat], b.Ideal))
		}
		if condDev.Cmp(b.WorstConditionalDeviation) > 0 {
			b.WorstConditionalDeviation.Set(condDev)
			res.DeviationWitness[cat] = clonePolicy(policy)
		}
	}
	return nil
}

func absRat(r *big.Rat) *big.Rat {
	if r.Sign() < 0 {
		return r.Neg(r)
	}
	return r
}

func clonePolicy(p Policy) Policy {
	out := make(Policy, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func (a *Analyzer) recordLine(policy Policy, d *Distribution) string {
	line := "rule"
	for _, v := range a.vars {
		line += "|" + v.id + "=" + policy[v.id]
	}
	line += "||failure=" + d.FailureMass.RatString()
	line += "|completion=" + d.CompletionMass.RatString()
	for _, cat := range a.categories() {
		line += "|" + cat + ".mass=" + d.SuccessMass[cat].RatString()
		if d.ConditionalShare != nil {
			line += "|" + cat + ".cond=" + d.ConditionalShare[cat].RatString()
		} else {
			line += "|" + cat + ".cond=undefined"
		}
	}
	return line
}
