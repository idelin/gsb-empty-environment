package migrationaudit

import "sort"

type feasibilityMemory struct {
	entries map[stateKey]*feasibilityEntry
	stats   SearchStats
	cap     int
}

type feasibilityEntry struct {
	key          stateKey
	feasible     bool
	chosenAction int
	legalActions []int
}

type optimizationMemory struct {
	entries map[stateKey]*optimizationEntry
	stats   SearchStats
	cap     int
}

type optimizationEntry struct {
	key                stateKey
	feasible           bool
	remainingSum       int
	chosenAction       int
	transitionExposure int
	successorKey       stateKey
	legalActions       []int
}

func Solve(problem Problem) (*Result, error) {
	indexed, err := prepareProblem(problem)
	if err != nil {
		return nil, err
	}
	if len(problem.ObserverGroups) == 0 {
		return nil, errNoObserverGroups()
	}
	initial := indexed.initialState()
	if !indexed.validatorAccepts(initial) {
		return &Result{Feasible: false, InfeasibleReason: "initial state has no accepted validator digest"}, nil
	}
	initialExposures := indexed.exposureAt(initial)
	initialPeak, _ := indexed.worstExposure(initialExposures)
	absolutePeak := len(indexed.accountOrder)
	if absolutePeak < initialPeak {
		absolutePeak = initialPeak
	}

	targetPeak := 0
	var lowerMemory *feasibilityMemory
	var upper *feasibilityMemory
	for targetPeak <= absolutePeak {
		memory := evaluateFeasibility(indexed, initial, targetPeak)
		if memory.entries[initial.structuredKey()].feasible {
			upper = memory
			break
		}
		lowerMemory = memory
		targetPeak++
	}
	if targetPeak == 0 {
		lowerMemory = nil
	}
	if targetPeak > absolutePeak {
		return &Result{Feasible: false, InfeasibleReason: "no topological sequence preserves an accepted validator digest"}, nil
	}

	optimalMemory := evaluateOptimization(indexed, initial, targetPeak)
	entry := optimalMemory.entries[initial.structuredKey()]
	if !entry.feasible {
		return nil, errInternalInconsistency()
	}
	order, states := reconstruct(indexed, initial, optimalMemory)
	result := indexed.buildResult(order, states, targetPeak, lowerMemory, optimalMemory, upper.stats, memoryStats(lowerMemory), optimalMemory.stats)
	return result, nil
}

func legalActionIDs(indexed *indexedProblem, state *auditState) []int {
	actions := indexed.legalActions(state)
	sort.Slice(actions, func(left, right int) bool {
		return indexed.problem.Actions[actions[left]].ID < indexed.problem.Actions[actions[right]].ID
	})
	return append([]int(nil), actions...)
}

func reconstruct(indexed *indexedProblem, initial *auditState, memory *optimizationMemory) ([]int, []*auditState) {
	order := make([]int, 0, len(indexed.problem.Actions))
	states := []*auditState{initial}
	state := initial
	for state.executed.count() < len(indexed.problem.Actions) {
		entry := memory.entries[state.structuredKey()]
		if entry == nil || entry.chosenAction < 0 {
			break
		}
		state = indexed.applyAction(state, entry.chosenAction)
		order = append(order, entry.chosenAction)
		states = append(states, state)
	}
	return order, states
}

func memoryStats(memory *feasibilityMemory) SearchStats {
	if memory == nil {
		return SearchStats{}
	}
	return memory.stats
}

func errNoObserverGroups() error {
	return &validationError{message: "at least one observer group is required"}
}

func errInternalInconsistency() error {
	return &validationError{message: "internal optimization inconsistency"}
}

type validationError struct {
	message string
}

func (err *validationError) Error() string {
	return err.message
}
