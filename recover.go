package netsec

import (
	"sort"
)

type componentAnalysis struct {
	unsatisfiable bool
	complete      bool
	exhausted     bool
}

func recoverWithOptions(records []Record, opts Options) (Result, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}
	if err := validateRecords(records); err != nil {
		return Result{}, err
	}

	result := newResult(records)
	budget := newSearchBudget(normalized.MaxSearchNodes)
	if len(records) == 0 {
		result.Status = Satisfiable
		result.Witnesses = [][]int{{}}
		return finishResult(result, budget), nil
	}

	p := newProblem(records)
	components := p.components()
	analysisComplete := true
	provenUnsat := false

	for _, active := range components {
		analysis := p.analyzeComponent(active, result.CandidateStates, budget)
		if analysis.exhausted {
			analysisComplete = false
			break
		}
		if analysis.unsatisfiable {
			provenUnsat = true
			analysisComplete = analysis.complete
			break
		}
		if !analysis.complete {
			analysisComplete = false
			break
		}
	}

	if provenUnsat {
		for i := range result.CandidateStates {
			for c := range result.CandidateStates[i] {
				result.CandidateStates[i][c] = CandidateImpossible
			}
		}
		result.ProvenUnsatisfiable = true
		if !normalized.SkipConflictAnalysis {
			conflict, complete, exhausted := p.findMinimalConflict(budget)
			result.Conflict = conflict
			if exhausted {
				analysisComplete = false
			}
			if !complete {
				analysisComplete = false
			}
		}
		if analysisComplete {
			result.Status = Unsatisfiable
		} else {
			result.Status = Unknown
		}
		fillCandidateViews(&result)
		return finishResult(result, budget), nil
	}

	if !analysisComplete || budget.exhausted {
		result.Status = Unknown
		fillCandidateViews(&result)
		result.Determined, result.DeterminedBytes = determineBytes(p, result.CandidateStates)
		return finishResult(result, budget), nil
	}

	activeAll := uint16(0)
	for i := range records {
		activeAll |= uint16(1) << uint(i)
	}
	domains := p.domainsFromStates(result.CandidateStates)
	witnessTarget := normalized.WitnessLimit + 1
	witnesses, exhausted := p.lexWitnesses(activeAll, domains, witnessTarget, budget)
	if exhausted {
		if len(witnesses) > normalized.WitnessLimit {
			result.Truncated = true
			witnesses = witnesses[:normalized.WitnessLimit]
		}
		result.Witnesses = witnesses
		result.Status = Unknown
		fillCandidateViews(&result)
		result.Determined, result.DeterminedBytes = determineBytes(p, result.CandidateStates)
		return finishResult(result, budget), nil
	}
	if len(witnesses) > normalized.WitnessLimit {
		result.Truncated = true
		witnesses = witnesses[:normalized.WitnessLimit]
	}
	if len(witnesses) == 0 {
		result.Status = Unknown
		result.BudgetExhausted = budget.exhausted
		fillCandidateViews(&result)
		return finishResult(result, budget), nil
	}
	result.Status = Satisfiable
	result.Witnesses = witnesses
	fillCandidateViews(&result)
	result.Determined, result.DeterminedBytes = determineBytes(p, result.CandidateStates)
	return finishResult(result, budget), nil
}

func newResult(records []Record) Result {
	result := Result{
		Status:             Unknown,
		Candidates:         make([][]int, len(records)),
		CandidateStates:    make([][]CandidateState, len(records)),
		ExcludedCandidates: make([][]int, len(records)),
		Determined:         make(map[int]byte),
	}
	for i, record := range records {
		result.CandidateStates[i] = make([]CandidateState, len(record.Candidates))
	}
	return result
}

func finishResult(result Result, budget *searchBudget) Result {
	result.SearchNodes = budget.used
	result.BudgetExhausted = budget.exhausted
	if result.ProvenUnsatisfiable {
		result.Determined = map[int]byte{}
		result.DeterminedBytes = nil
	}
	return result
}

func fillCandidateViews(result *Result) {
	for i := range result.CandidateStates {
		result.Candidates[i] = nil
		result.ExcludedCandidates[i] = nil
		for c, state := range result.CandidateStates[i] {
			switch state {
			case CandidateFeasible:
				result.Candidates[i] = append(result.Candidates[i], c)
			case CandidateImpossible:
				result.ExcludedCandidates[i] = append(result.ExcludedCandidates[i], c)
			}
		}
	}
}

func (p *problem) components() []uint16 {
	visited := make([]bool, p.n)
	components := make([]uint16, 0, p.n)
	for seed := 0; seed < p.n; seed++ {
		if visited[seed] {
			continue
		}
		active := uint16(0)
		queue := []int{seed}
		visited[seed] = true
		for head := 0; head < len(queue); head++ {
			i := queue[head]
			active |= uint16(1) << uint(i)
			for _, j := range p.neighbors[i] {
				if !visited[j] {
					visited[j] = true
					queue = append(queue, j)
				}
			}
		}
		components = append(components, active)
	}
	return components
}

func (p *problem) analyzeComponent(active uint16, states [][]CandidateState, budget *searchBudget) componentAnalysis {
	members := activeIndices(active, p.n)
	if len(members) == 0 {
		return componentAnalysis{complete: true}
	}
	if len(members) == 1 {
		i := members[0]
		if len(p.records[i].Candidates) == 0 {
			return componentAnalysis{unsatisfiable: true, complete: true}
		}
		for c := range states[i] {
			states[i][c] = CandidateFeasible
		}
		return componentAnalysis{complete: true}
	}

	domains := p.allDomains()
	consistent, exhausted := p.ac3(active, domains, budget)
	if exhausted {
		return componentAnalysis{exhausted: true}
	}
	if !consistent {
		return componentAnalysis{unsatisfiable: true, complete: true}
	}
	base := cloneDomains(domains)
	for _, i := range members {
		for c := range states[i] {
			if base[i]&(uint64(1)<<uint(c)) == 0 {
				states[i][c] = CandidateImpossible
			}
		}
	}

	solution, found, exhausted := p.firstSolution(active, base, budget)
	if exhausted {
		return componentAnalysis{exhausted: true}
	}
	if !found {
		return componentAnalysis{unsatisfiable: true, complete: true}
	}
	p.markFeasible(states, solution)

	for _, i := range members {
		for c, state := range states[i] {
			if state != CandidateUnknown {
				continue
			}
			solution, found, exhausted := p.firstSolutionFixed(active, base, i, c, budget)
			if exhausted {
				return componentAnalysis{exhausted: true}
			}
			if !found {
				states[i][c] = CandidateImpossible
				continue
			}
			states[i][c] = CandidateFeasible
			p.markFeasible(states, solution)
		}
	}

	complete := true
	for _, i := range members {
		for _, state := range states[i] {
			if state == CandidateUnknown {
				complete = false
				break
			}
		}
	}
	return componentAnalysis{complete: complete}
}

func (p *problem) markFeasible(states [][]CandidateState, solution []int) {
	for i, candidate := range solution {
		if candidate >= 0 && candidate < len(states[i]) {
			states[i][candidate] = CandidateFeasible
		}
	}
}

func determineBytes(p *problem, states [][]CandidateState) (map[int]byte, []KeyByte) {
	positions := make(map[int]struct{})
	for _, record := range p.records {
		for pos := record.Offset; pos < record.Offset+len(record.Ciphertext); pos++ {
			positions[pos] = struct{}{}
		}
	}
	ordered := make([]int, 0, len(positions))
	for pos := range positions {
		ordered = append(ordered, pos)
	}
	sort.Ints(ordered)

	determined := make(map[int]byte)
	for _, pos := range ordered {
		var value byte
		haveValue := false
		conflicted := false
		for i, record := range p.records {
			if pos < record.Offset || pos >= record.Offset+len(record.Ciphertext) {
				continue
			}
			for c, state := range states[i] {
				if state == CandidateImpossible {
					continue
				}
				candidateValue := p.keys[i][c][pos-record.Offset]
				if !haveValue {
					value = candidateValue
					haveValue = true
				} else if candidateValue != value {
					conflicted = true
				}
			}
		}
		if haveValue && !conflicted {
			determined[pos] = value
		}
	}
	orderedBytes := make([]KeyByte, 0, len(determined))
	for pos, value := range determined {
		orderedBytes = append(orderedBytes, KeyByte{Offset: pos, Value: value})
	}
	sort.Slice(orderedBytes, func(i, j int) bool {
		return orderedBytes[i].Offset < orderedBytes[j].Offset
	})
	return determined, orderedBytes
}
