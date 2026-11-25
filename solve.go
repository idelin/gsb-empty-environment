package upgradeplan

import (
	"fmt"
	"sort"
)

type solveResult struct {
	node    *PlanNode
	failure *Failure
	rounds  int
}

type planner struct {
	np             *normalizedProblem
	memo           map[string]*solveResult
	transitionMemo map[string]map[int]*Belief
	outcomeMemo    map[string][]Outcome
}

// Solve returns a shortest worst-case count-conditional plan. If no plan exists
// within Problem.MaxRounds, the returned error is *InfeasibleError and contains
// failure evidence for every candidate broadcast at the root and failing child
// beliefs.
func Solve(problem Problem) (*Plan, error) {
	np, err := NormalizeProblem(problem)
	if err != nil {
		return nil, err
	}
	worker := &planner{
		np:             np,
		memo:           make(map[string]*solveResult),
		transitionMemo: make(map[string]map[int]*Belief),
		outcomeMemo:    make(map[string][]Outcome),
	}
	result := worker.solve(np.belief, np.maxRounds)
	if result.failure != nil {
		return nil, &InfeasibleError{Failure: result.failure}
	}
	return &Plan{WorstRounds: result.rounds, Root: result.node}, nil
}

func (p *planner) solve(belief *Belief, remaining int) *solveResult {
	key := fmt.Sprintf("%d|%s", remaining, belief.Key())
	if cached, ok := p.memo[key]; ok {
		return cached
	}

	if _, _, reason := firstDataLossWorld(belief); reason != "" {
		result := &solveResult{failure: dataLossFailure(belief, remaining)}
		p.memo[key] = result
		return result
	}

	allDone := len(belief.Worlds) > 0
	for _, fleet := range belief.Worlds {
		if ok, _ := p.np.goalFleet(fleet); !ok {
			allDone = false
			break
		}
	}
	if allDone {
		node := &PlanNode{Terminal: true, WorstRemainingRounds: 0}
		result := &solveResult{node: node, rounds: 0}
		p.memo[key] = result
		return result
	}

	if remaining <= 0 {
		result := &solveResult{failure: exhaustedFailure(p.np, belief, remaining)}
		p.memo[key] = result
		return result
	}

	releases := append([]Release{}, p.np.releases...)
	sort.Slice(releases, func(i, j int) bool { return releases[i].ID < releases[j].ID })

	var best *solveResult
	var candidateFailures []CandidateFailure

	for _, release := range releases {
		children := p.transitionBelief(belief, release)
		counts := make([]int, 0, len(children))
		for count := range children {
			counts = append(counts, count)
		}
		sort.Ints(counts)

		branches := make(map[int]*PlanNode, len(counts))
		var branchFailures []BranchFailure
		candidateRounds := 0
		candidateOK := true

		for _, count := range counts {
			childBelief := children[count]
			childResult := p.solve(childBelief, remaining-1)
			if childResult.failure != nil {
				candidateOK = false
				branchFailures = append(branchFailures, BranchFailure{
					Count:     count,
					BeliefKey: childBelief.Key(),
					Failure:   childResult.failure,
				})
				continue
			}
			branches[count] = childResult.node
			if childResult.rounds > candidateRounds {
				candidateRounds = childResult.rounds
			}
		}

		if candidateOK {
			rounds := 1 + candidateRounds
			node := &PlanNode{
				Broadcast:            &Broadcast{ReleaseID: release.ID, Package: release.Package},
				Branches:             branches,
				WorstRemainingRounds: rounds,
			}
			candidate := &solveResult{node: node, rounds: rounds}
			if best == nil || candidate.rounds < best.rounds {
				best = candidate
			}
		} else {
			candidateFailures = append(candidateFailures, CandidateFailure{
				ReleaseID: release.ID,
				Package:   release.Package,
				Reason:    fmt.Sprintf("%d visible-count branch(es) have no safe completion within %d further rounds", len(branchFailures), remaining-1),
				Branches:  branchFailures,
			})
		}
	}

	if best != nil {
		// Releases are sorted by ID above; replacing best only on a strictly
		// smaller round count keeps lexicographically smallest ties deterministic.
		p.memo[key] = best
		return best
	}

	failure := &Failure{
		Reason:          ReasonNoBroadcast,
		RemainingRounds: remaining,
		BeliefKey:       belief.Key(),
		Candidates:      candidateFailures,
	}
	if len(candidateFailures) == 0 {
		failure.Witnesses = exhaustedFailure(p.np, belief, remaining).Witnesses
		failure.Reason = ReasonRoundsExhausted
	}
	result := &solveResult{failure: failure}
	p.memo[key] = result
	return result
}

func (p *planner) transitionBelief(belief *Belief, release Release) map[int]*Belief {
	cacheKey := belief.Key() + "\x00" + release.ID
	if cached, ok := p.transitionMemo[cacheKey]; ok {
		return cached
	}

	worldsByCount := make(map[int][][]DeviceState)
	for _, fleet := range belief.Worlds {
		outcomes, err := p.outcomes(fleet, release)
		if err != nil {
			continue
		}
		for _, outcome := range outcomes {
			worldsByCount[outcome.SuccessCount] = append(
				worldsByCount[outcome.SuccessCount],
				[]DeviceState(outcome.Fleet),
			)
		}
	}

	result := make(map[int]*Belief, len(worldsByCount))
	for count, worlds := range worldsByCount {
		result[count] = NewBelief(worlds...)
	}
	p.transitionMemo[cacheKey] = result
	return result
}

func (p *planner) outcomes(fleet Fleet, release Release) ([]Outcome, error) {
	cacheKey := fleet.Key() + "\x00" + release.ID
	if cached, ok := p.outcomeMemo[cacheKey]; ok {
		return cached, nil
	}
	outcomes, err := enumerateCanonicalOutcomes(fleet, release)
	if err != nil {
		return nil, err
	}
	p.outcomeMemo[cacheKey] = outcomes
	return outcomes, nil
}
