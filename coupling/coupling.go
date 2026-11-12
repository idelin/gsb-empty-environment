// Package coupling computes exact rational couplings between two finite
// rational distributions and produces independently checkable certificates.
//
// A coupling problem consists of two finite (sub-)distributions with
// rational weights and a set of allowed pairing edges supplied by the
// caller. Solve computes a maximum-mass joint distribution supported on
// the allowed edges, together with a minimum cut that witnesses
// optimality. Verify re-checks a Result against a Problem using only
// exact rational arithmetic, so a skeptical host can validate untrusted
// solver output without re-running the solver.
//
// Sub-distributions (total mass below 1) are supported as-is: the solver
// never normalizes weights. It pairs as much mass as the constraints
// allow and reports the unpaired remainder on each side.
package coupling

import (
	"errors"
	"fmt"
	"math/big"
)

// Problem describes two finite rational (sub-)distributions and the
// allowed pairing edges between their support points.
type Problem struct {
	// Left and Right hold the point masses of the two distributions.
	// Every weight must be non-negative. Weights are used as given;
	// they are never rescaled to sum to one.
	Left  []*big.Rat
	Right []*big.Rat
	// Edges lists the allowed pairs (i, j) with i indexing Left and
	// j indexing Right. Each pair may appear at most once.
	Edges [][2]int
}

// EdgeFlow is the rational mass assigned to one allowed edge.
type EdgeFlow struct {
	Left   int
	Right  int
	Amount *big.Rat
}

// Cut is the optimality witness: the source side of a minimum cut in
// the coupling flow network. A left point outside the cut contributes
// its weight to the cut capacity; a right point inside the cut
// contributes its weight.
type Cut struct {
	LeftInS  []bool
	RightInS []bool
}

// Result is the solver output: a feasible rational flow, the paired and
// unpaired masses, and a cut certificate proving the flow is maximum.
type Result struct {
	// Flows is aligned with Problem.Edges: Flows[k] carries the mass
	// assigned to Edges[k].
	Flows []EdgeFlow
	// Paired is the total mass placed on allowed edges.
	Paired *big.Rat
	// UnpairedLeft is TotalLeft - Paired, the mass no coupling
	// supported on the allowed edges can match on the left side.
	UnpairedLeft *big.Rat
	// UnpairedRight is TotalRight - Paired.
	UnpairedRight *big.Rat
	// Cut witnesses that no feasible flow pairs more than Paired.
	Cut Cut
}

// NewProblem validates the inputs and returns a coupling problem that
// owns copies of them.
func NewProblem(left, right []*big.Rat, edges [][2]int) (*Problem, error) {
	p := &Problem{
		Left:  make([]*big.Rat, len(left)),
		Right: make([]*big.Rat, len(right)),
		Edges: append([][2]int(nil), edges...),
	}
	for i, w := range left {
		if w == nil {
			return nil, fmt.Errorf("coupling: left weight %d is nil", i)
		}
		p.Left[i] = new(big.Rat).Set(w)
	}
	for j, w := range right {
		if w == nil {
			return nil, fmt.Errorf("coupling: right weight %d is nil", j)
		}
		p.Right[j] = new(big.Rat).Set(w)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Problem) validate() error {
	if len(p.Left) == 0 || len(p.Right) == 0 {
		return errors.New("coupling: both supports must be non-empty")
	}
	for i, w := range p.Left {
		if w == nil || w.Sign() < 0 {
			return fmt.Errorf("coupling: left weight %d is nil or negative", i)
		}
	}
	for j, w := range p.Right {
		if w == nil || w.Sign() < 0 {
			return fmt.Errorf("coupling: right weight %d is nil or negative", j)
		}
	}
	seen := make(map[[2]int]bool, len(p.Edges))
	for k, e := range p.Edges {
		if e[0] < 0 || e[0] >= len(p.Left) || e[1] < 0 || e[1] >= len(p.Right) {
			return fmt.Errorf("coupling: edge %d index (%d, %d) out of range", k, e[0], e[1])
		}
		if seen[e] {
			return fmt.Errorf("coupling: duplicate edge (%d, %d)", e[0], e[1])
		}
		seen[e] = true
	}
	return nil
}

// TotalLeft returns the sum of the left weights, as given.
func (p *Problem) TotalLeft() *big.Rat {
	return sumRats(p.Left)
}

// TotalRight returns the sum of the right weights, as given.
func (p *Problem) TotalRight() *big.Rat {
	return sumRats(p.Right)
}

func sumRats(ws []*big.Rat) *big.Rat {
	total := new(big.Rat)
	for _, w := range ws {
		total.Add(total, w)
	}
	return total
}

// IsComplete reports whether r pairs all mass on both sides, i.e. the
// flows form a genuine joint distribution with the prescribed marginals.
func (p *Problem) IsComplete(r *Result) bool {
	if r == nil || r.Paired == nil {
		return false
	}
	return r.Paired.Cmp(p.TotalLeft()) == 0 && r.Paired.Cmp(p.TotalRight()) == 0
}

// Solve computes a maximum-mass rational coupling of p supported on the
// allowed edges, plus a minimum cut certifying optimality. Weights are
// never normalized: if the two sides have different total mass, or the
// allowed edges cannot carry all of it, the unpaired remainder is
// reported in UnpairedLeft and UnpairedRight.
func Solve(p *Problem) (*Result, error) {
	if p == nil {
		return nil, errors.New("coupling: nil problem")
	}
	if err := p.validate(); err != nil {
		return nil, err
	}

	nL, nR := len(p.Left), len(p.Right)

	// Scale all weights to integers by the LCM of their denominators so
	// the max-flow computation is exact integer arithmetic.
	scale := big.NewInt(1)
	tmp := new(big.Int)
	for _, w := range p.Left {
		d := w.Denom()
		tmp.GCD(nil, nil, scale, d)
		scale.Mul(scale, d).Quo(scale, tmp)
	}
	for _, w := range p.Right {
		d := w.Denom()
		tmp.GCD(nil, nil, scale, d)
		scale.Mul(scale, d).Quo(scale, tmp)
	}
	toInt := func(w *big.Rat) *big.Int {
		q := new(big.Int).Quo(scale, w.Denom())
		return q.Mul(q, w.Num())
	}

	// Layered network: source -> left points -> right points -> sink.
	// Left/right edge capacities are the scaled weights; allowed pairing
	// edges get a capacity larger than any feasible flow.
	s, t := 0, nL+nR+1
	n := t + 1
	res := make([][]*big.Int, n)
	for i := range res {
		res[i] = make([]*big.Int, n)
		for j := range res[i] {
			res[i][j] = new(big.Int)
		}
	}
	leftNode := func(i int) int { return 1 + i }
	rightNode := func(j int) int { return 1 + nL + j }

	totalLeftInt := new(big.Int)
	for i, w := range p.Left {
		c := toInt(w)
		res[s][leftNode(i)].Set(c)
		totalLeftInt.Add(totalLeftInt, c)
	}
	for j, w := range p.Right {
		res[rightNode(j)][t].Set(toInt(w))
	}
	for _, e := range p.Edges {
		res[leftNode(e[0])][rightNode(e[1])].Set(totalLeftInt)
	}

	flow, reach := maxFlow(n, s, t, res)

	scaleRat := new(big.Rat).SetInt(scale)
	toRat := func(x *big.Int) *big.Rat {
		r := new(big.Rat).SetInt(x)
		return r.Quo(r, scaleRat)
	}

	result := &Result{
		Flows: make([]EdgeFlow, len(p.Edges)),
		Cut: Cut{
			LeftInS:  make([]bool, nL),
			RightInS: make([]bool, nR),
		},
	}
	pairedInt := new(big.Int)
	for k, e := range p.Edges {
		f := flow[leftNode(e[0])][rightNode(e[1])]
		pairedInt.Add(pairedInt, f)
		result.Flows[k] = EdgeFlow{Left: e[0], Right: e[1], Amount: toRat(f)}
	}
	for i := range p.Left {
		result.Cut.LeftInS[i] = reach[leftNode(i)]
	}
	for j := range p.Right {
		result.Cut.RightInS[j] = reach[rightNode(j)]
	}

	result.Paired = toRat(pairedInt)
	result.UnpairedLeft = new(big.Rat).Sub(p.TotalLeft(), result.Paired)
	result.UnpairedRight = new(big.Rat).Sub(p.TotalRight(), result.Paired)
	return result, nil
}

// Verify checks that r is a valid, optimal coupling certificate for p.
// It confirms the flow is feasible (non-negative, bounded by both
// marginals), that the reported masses are consistent, and that the cut
// is a valid finite cut whose capacity equals the paired mass. By
// max-flow/min-cut duality the last condition proves no feasible
// coupling pairs more mass, so the unpaired masses are minimal.
func Verify(p *Problem, r *Result) error {
	if p == nil || r == nil {
		return errors.New("coupling: nil problem or result")
	}
	if err := p.validate(); err != nil {
		return err
	}
	nL, nR := len(p.Left), len(p.Right)

	if len(r.Flows) != len(p.Edges) {
		return fmt.Errorf("coupling: got %d flows for %d edges", len(r.Flows), len(p.Edges))
	}
	leftOut := make([]*big.Rat, nL)
	rightIn := make([]*big.Rat, nR)
	for i := range leftOut {
		leftOut[i] = new(big.Rat)
	}
	for j := range rightIn {
		rightIn[j] = new(big.Rat)
	}
	paired := new(big.Rat)
	for k, ef := range r.Flows {
		e := p.Edges[k]
		if ef.Left != e[0] || ef.Right != e[1] {
			return fmt.Errorf("coupling: flow %d addresses edge (%d, %d), want (%d, %d)",
				k, ef.Left, ef.Right, e[0], e[1])
		}
		if ef.Amount == nil || ef.Amount.Sign() < 0 {
			return fmt.Errorf("coupling: flow %d is nil or negative", k)
		}
		leftOut[e[0]].Add(leftOut[e[0]], ef.Amount)
		rightIn[e[1]].Add(rightIn[e[1]], ef.Amount)
		paired.Add(paired, ef.Amount)
	}

	// Marginal conservation: the flow out of each point is bounded by
	// its prescribed mass; nothing is created, lost, or normalized.
	for i := range p.Left {
		if leftOut[i].Cmp(p.Left[i]) > 0 {
			return fmt.Errorf("coupling: left marginal %d exceeded: flow %s > weight %s",
				i, leftOut[i], p.Left[i])
		}
	}
	for j := range p.Right {
		if rightIn[j].Cmp(p.Right[j]) > 0 {
			return fmt.Errorf("coupling: right marginal %d exceeded: flow %s > weight %s",
				j, rightIn[j], p.Right[j])
		}
	}

	if r.Paired == nil || r.Paired.Cmp(paired) != 0 {
		return fmt.Errorf("coupling: reported paired mass %v does not match flow sum %v", r.Paired, paired)
	}
	if r.UnpairedLeft == nil || r.UnpairedLeft.Cmp(new(big.Rat).Sub(p.TotalLeft(), paired)) != 0 {
		return errors.New("coupling: reported unpaired left mass is inconsistent")
	}
	if r.UnpairedRight == nil || r.UnpairedRight.Cmp(new(big.Rat).Sub(p.TotalRight(), paired)) != 0 {
		return errors.New("coupling: reported unpaired right mass is inconsistent")
	}

	if len(r.Cut.LeftInS) != nL || len(r.Cut.RightInS) != nR {
		return errors.New("coupling: cut has wrong dimension")
	}
	for _, e := range p.Edges {
		if r.Cut.LeftInS[e[0]] && !r.Cut.RightInS[e[1]] {
			return fmt.Errorf("coupling: cut crosses allowed edge (%d, %d): not a finite cut", e[0], e[1])
		}
	}
	capacity := new(big.Rat)
	for i := range p.Left {
		if !r.Cut.LeftInS[i] {
			capacity.Add(capacity, p.Left[i])
		}
	}
	for j := range p.Right {
		if r.Cut.RightInS[j] {
			capacity.Add(capacity, p.Right[j])
		}
	}
	if capacity.Cmp(paired) != 0 {
		return fmt.Errorf("coupling: cut capacity %s does not equal paired mass %s", capacity, paired)
	}
	return nil
}
