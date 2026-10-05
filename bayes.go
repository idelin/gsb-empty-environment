// Package bayes implements exact Bayesian evidence updating over a finite
// set of hypotheses using rational arithmetic.
//
// Evidence is supplied in groups: each group carries, for every hypothesis,
// the JOINT likelihood of the whole group under that hypothesis. Observations
// within a group are never multiplied together as if they were independent;
// the caller provides the joint probability directly.
package bayes

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrContradiction is returned when the total likelihood of the evidence is
// zero under every hypothesis, i.e. the evidence is jointly impossible.
var ErrContradiction = errors.New("bayes: contradiction: total likelihood is zero")

// Rat is a convenience constructor for exact rational values.
func Rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

// Model holds a normalized rational prior plus a set of named evidence
// groups. Hypotheses are indexed 0..n-1 according to the prior slice.
type Model struct {
	prior    []*big.Rat
	evidence map[string][]*big.Rat
	order    []string
}

// NewModel normalizes the given non-negative rational prior. The prior may
// be unnormalized as long as its total is positive.
func NewModel(prior []*big.Rat) (*Model, error) {
	if len(prior) == 0 {
		return nil, errors.New("bayes: prior must not be empty")
	}
	p := make([]*big.Rat, len(prior))
	total := new(big.Rat)
	for i, v := range prior {
		if v == nil || v.Sign() < 0 {
			return nil, fmt.Errorf("bayes: prior[%d] must be a non-negative rational", i)
		}
		p[i] = new(big.Rat).Set(v)
		total.Add(total, v)
	}
	if total.Sign() == 0 {
		return nil, errors.New("bayes: prior sums to zero")
	}
	for i := range p {
		p[i].Quo(p[i], total)
	}
	return &Model{prior: p, evidence: make(map[string][]*big.Rat)}, nil
}

// NumHypotheses returns the number of hypotheses.
func (m *Model) NumHypotheses() int {
	return len(m.prior)
}

// AddEvidence registers an evidence group under id. likelihood[h] is the
// joint probability of the whole group given hypothesis h. Adding an id
// that already exists is a no-op (duplicate evidence counts only once) and
// reports added == false.
func (m *Model) AddEvidence(id string, likelihood []*big.Rat) (added bool, err error) {
	if _, dup := m.evidence[id]; dup {
		return false, nil
	}
	cp, err := checkLikelihood(id, likelihood, len(m.prior))
	if err != nil {
		return false, err
	}
	m.evidence[id] = cp
	m.order = append(m.order, id)
	return true, nil
}

// Retract removes the evidence group registered under id, restoring the
// posterior to what it would be without that group. It reports whether the
// id was present.
func (m *Model) Retract(id string) bool {
	if _, ok := m.evidence[id]; !ok {
		return false
	}
	delete(m.evidence, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return true
}

// Posterior returns the exact posterior P(H_h | evidence) for each
// hypothesis. It returns ErrContradiction if the combined evidence has zero
// total likelihood.
func (m *Model) Posterior() ([]*big.Rat, error) {
	w := make([]*big.Rat, len(m.prior))
	total := new(big.Rat)
	for h := range w {
		w[h] = new(big.Rat).Set(m.prior[h])
		for _, id := range m.order {
			w[h].Mul(w[h], m.evidence[id][h])
		}
		total.Add(total, w[h])
	}
	if total.Sign() == 0 {
		return nil, ErrContradiction
	}
	for h := range w {
		w[h].Quo(w[h], total)
	}
	return w, nil
}

// Observation describes a candidate new observation: OutcomeProb[o][h] is
// the probability of outcome o under hypothesis h. For each hypothesis the
// outcome probabilities must sum to 1.
type Observation struct {
	OutcomeProb [][]*big.Rat
}

// ExpectedError returns the expected misjudgment rate of an observation
// given belief w: after observing the outcome one picks the most probable
// hypothesis, so the error rate is 1 - sum_o max_h w[h]*P(o|h).
func ExpectedError(w []*big.Rat, obs Observation) (*big.Rat, error) {
	if len(obs.OutcomeProb) == 0 {
		return nil, errors.New("bayes: observation has no outcomes")
	}
	colsum := make([]*big.Rat, len(w))
	for h := range colsum {
		colsum[h] = new(big.Rat)
	}
	gain := new(big.Rat)
	for o, row := range obs.OutcomeProb {
		if len(row) != len(w) {
			return nil, fmt.Errorf("bayes: outcome %d has %d probabilities, want %d", o, len(row), len(w))
		}
		best := new(big.Rat)
		for h, p := range row {
			if p == nil || p.Sign() < 0 {
				return nil, fmt.Errorf("bayes: outcome %d hypothesis %d: negative probability", o, h)
			}
			colsum[h].Add(colsum[h], p)
			joint := new(big.Rat).Mul(w[h], p)
			if joint.Cmp(best) > 0 {
				best.Set(joint)
			}
		}
		gain.Add(gain, best)
	}
	one := big.NewRat(1, 1)
	for h, s := range colsum {
		if s.Cmp(one) != 0 {
			return nil, fmt.Errorf("bayes: outcome probabilities under hypothesis %d sum to %s, not 1", h, s.RatString())
		}
	}
	return one.Sub(one, gain), nil
}

// BestObservation selects the candidate observation with the lowest expected
// misjudgment rate under the current posterior. Ties are broken by the
// lowest candidate index. It returns the chosen index and its expected
// error rate.
func (m *Model) BestObservation(candidates []Observation) (best int, minErr *big.Rat, err error) {
	if len(candidates) == 0 {
		return -1, nil, errors.New("bayes: no candidate observations")
	}
	post, err := m.Posterior()
	if err != nil {
		return -1, nil, err
	}
	best = -1
	for i, c := range candidates {
		e, err := ExpectedError(post, c)
		if err != nil {
			return -1, nil, fmt.Errorf("bayes: candidate %d: %v", i, err)
		}
		if best < 0 || e.Cmp(minErr) < 0 {
			best, minErr = i, e
		}
	}
	return best, minErr, nil
}

func checkLikelihood(id string, likelihood []*big.Rat, n int) ([]*big.Rat, error) {
	if len(likelihood) != n {
		return nil, fmt.Errorf("bayes: evidence %q has %d likelihoods, want %d", id, len(likelihood), n)
	}
	cp := make([]*big.Rat, n)
	for h, v := range likelihood {
		if v == nil || v.Sign() < 0 {
			return nil, fmt.Errorf("bayes: evidence %q: likelihood[%d] must be non-negative", id, h)
		}
		cp[h] = new(big.Rat).Set(v)
	}
	return cp, nil
}
