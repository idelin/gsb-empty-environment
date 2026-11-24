package migrationaudit

import (
	"fmt"
	"sort"
)

func VerifyResult(problem Problem, result *Result) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	if !result.Feasible {
		return nil
	}
	if result.Certificate == nil {
		return fmt.Errorf("result is missing a certificate")
	}
	return VerifyCertificate(problem, result.Certificate)
}

func VerifyCertificate(problem Problem, certificate *Certificate) error {
	indexed, err := prepareProblem(problem)
	if err != nil {
		return err
	}
	if certificate == nil {
		return fmt.Errorf("certificate is nil")
	}
	if len(problem.ObserverGroups) == 0 {
		return fmt.Errorf("at least one observer group is required")
	}
	initial := indexed.initialState()
	if certificate.InitialStateKey != initial.key() {
		return fmt.Errorf("initial state key does not match the problem")
	}
	if !indexed.validatorAccepts(initial) {
		return fmt.Errorf("initial state has no accepted validator digest")
	}
	if err := verifyLowerCap(indexed, initial, certificate); err != nil {
		return err
	}
	return verifyOptimization(indexed, initial, certificate)
}

func verifyLowerCap(indexed *indexedProblem, initial *auditState, certificate *Certificate) error {
	if certificate.TargetPeak == 0 {
		if certificate.LowerCap != -1 || len(certificate.LowerCapFeasibility) != 0 {
			return fmt.Errorf("lower-cap proof is invalid for a zero peak")
		}
		return nil
	}
	expectedCap := certificate.TargetPeak - 1
	if certificate.LowerCap != expectedCap {
		return fmt.Errorf("lower cap is %d, expected %d", certificate.LowerCap, expectedCap)
	}
	entry, ok := certificate.LowerCapFeasibility[initial.key()]
	if !ok {
		return fmt.Errorf("lower-cap proof is missing the initial state")
	}
	if entry.Feasible {
		return fmt.Errorf("lower-cap proof incorrectly marks the initial state feasible")
	}
	if err := verifyFeasibilityClosure(indexed, initial, certificate.LowerCap, certificate.LowerCapFeasibility); err != nil {
		return err
	}
	for key, entry := range certificate.LowerCapFeasibility {
		state, ok := indexed.decodeState(key)
		if !ok {
			return fmt.Errorf("lower-cap state key is invalid: %s", key)
		}
		if entry.StateKey != key {
			return fmt.Errorf("lower-cap entry key does not match its map key")
		}
		legal := indexed.legalActions(state)
		if !sameActions(indexed, legal, entry.LegalActionIDs) {
			return fmt.Errorf("lower-cap legal actions do not match for state %s", key)
		}
		if state.executed.count() == len(indexed.problem.Actions) {
			if !entry.Feasible || entry.ChosenActionID != -1 {
				return fmt.Errorf("terminal lower-cap state is incorrectly recorded")
			}
			continue
		}
		if entry.Feasible {
			chosen := findActionByID(indexed, entry.ChosenActionID)
			if chosen < 0 || !containsAction(legal, chosen) {
				return fmt.Errorf("lower-cap chosen action is not legal")
			}
			next := indexed.applyAction(state, chosen)
			successor, ok := certificate.LowerCapFeasibility[next.key()]
			if !ok || !successor.Feasible {
				return fmt.Errorf("lower-cap feasible transition lacks a feasible successor")
			}
			if err := verifyPostAction(indexed, next, certificate.LowerCap); err != nil {
				return err
			}
		} else {
			if entry.ChosenActionID != -1 {
				return fmt.Errorf("infeasible lower-cap state has a chosen action")
			}
			if err := verifyInfeasibleTransitions(indexed, state, legal, certificate.LowerCapFeasibility, certificate.LowerCap); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyOptimization(indexed *indexedProblem, initial *auditState, certificate *Certificate) error {
	entry, ok := certificate.Optimization[initial.key()]
	if !ok {
		return fmt.Errorf("optimization proof is missing the initial state")
	}
	if !entry.Feasible {
		return fmt.Errorf("optimization proof incorrectly marks the initial state infeasible")
	}
	if err := verifyOptimizationClosure(indexed, initial, certificate); err != nil {
		return err
	}
	for key, entry := range certificate.Optimization {
		state, ok := indexed.decodeState(key)
		if !ok {
			return fmt.Errorf("optimization state key is invalid: %s", key)
		}
		if entry.StateKey != key {
			return fmt.Errorf("optimization entry key does not match its map key")
		}
		legal := indexed.legalActions(state)
		if !sameActions(indexed, legal, entry.LegalActionIDs) {
			return fmt.Errorf("optimization legal actions do not match for state %s", key)
		}
		if state.executed.count() == len(indexed.problem.Actions) {
			if !entry.Feasible || entry.RemainingExposureSum != 0 || entry.ChosenActionID != -1 || entry.SuccessorStateKey != "" {
				return fmt.Errorf("terminal optimization state is incorrectly recorded")
			}
			continue
		}
		if !entry.Feasible {
			return fmt.Errorf("the target-peak optimization table contains an infeasible reachable state")
		}
		chosen := findActionByID(indexed, entry.ChosenActionID)
		if chosen < 0 || !containsAction(legal, chosen) {
			return fmt.Errorf("optimization chosen action is not legal")
		}
		next := indexed.applyAction(state, chosen)
		if next.key() != entry.SuccessorStateKey {
			return fmt.Errorf("optimization successor key does not match the chosen transition")
		}
		successor, ok := certificate.Optimization[next.key()]
		if !ok || !successor.Feasible {
			return fmt.Errorf("optimization successor is missing or infeasible")
		}
		exposures := indexed.exposureAt(next)
		worst, _ := indexed.worstExposure(exposures)
		if worst != entry.TransitionExposure {
			return fmt.Errorf("transition exposure does not match independently recomputed value")
		}
		if worst > certificate.TargetPeak {
			return fmt.Errorf("optimization transition exceeds the target peak")
		}
		if entry.RemainingExposureSum != worst+successor.RemainingExposureSum {
			return fmt.Errorf("optimization recurrence does not hold for state %s", key)
		}
		if err := verifyPostAction(indexed, next, certificate.TargetPeak); err != nil {
			return err
		}
		if err := verifyOptimalChoice(indexed, state, legal, chosen, worst, certificate); err != nil {
			return err
		}
	}
	return nil
}

func verifyFeasibilityClosure(indexed *indexedProblem, initial *auditState, cap int, entries map[string]FeasibilityCertificateEntry) error {
	queue := []*auditState{initial}
	seen := map[string]bool{initial.key(): true}
	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		if _, ok := entries[state.key()]; !ok {
			return fmt.Errorf("feasibility proof is missing reachable state %s", state.key())
		}
		for _, actionIndex := range indexed.legalActions(state) {
			next := indexed.applyAction(state, actionIndex)
			if !indexed.validatorAccepts(next) {
				continue
			}
			exposures := indexed.exposureAt(next)
			worst, _ := indexed.worstExposure(exposures)
			if worst > cap || seen[next.key()] {
				continue
			}
			seen[next.key()] = true
			queue = append(queue, next)
		}
	}
	for key := range entries {
		if !seen[key] {
			return fmt.Errorf("feasibility proof contains unreachable state %s", key)
		}
	}
	return nil
}

func verifyOptimizationClosure(indexed *indexedProblem, initial *auditState, certificate *Certificate) error {
	queue := []*auditState{initial}
	seen := map[string]bool{initial.key(): true}
	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		if state.executed.count() == len(indexed.problem.Actions) {
			continue
		}
		for _, actionIndex := range indexed.legalActions(state) {
			next := indexed.applyAction(state, actionIndex)
			if !indexed.validatorAccepts(next) || seen[next.key()] {
				continue
			}
			exposures := indexed.exposureAt(next)
			worst, _ := indexed.worstExposure(exposures)
			if worst > certificate.TargetPeak {
				continue
			}
			entry, ok := certificate.Optimization[next.key()]
			if !ok || !entry.Feasible {
				return fmt.Errorf("optimization proof is missing reachable state %s", next.key())
			}
			seen[next.key()] = true
			queue = append(queue, next)
		}
	}
	for key := range certificate.Optimization {
		if !seen[key] {
			return fmt.Errorf("optimization proof contains unreachable state %s", key)
		}
	}
	return nil
}

func verifyOptimalChoice(indexed *indexedProblem, state *auditState, legal []int, chosen int, chosenWorst int, certificate *Certificate) error {
	chosenID := indexed.problem.Actions[chosen].ID
	for _, actionIndex := range legal {
		next := indexed.applyAction(state, actionIndex)
		if !indexed.validatorAccepts(next) {
			continue
		}
		exposures := indexed.exposureAt(next)
		worst, _ := indexed.worstExposure(exposures)
		if worst > certificate.TargetPeak {
			continue
		}
		successor, ok := certificate.Optimization[next.key()]
		if !ok || !successor.Feasible {
			return fmt.Errorf("feasible successor is missing from optimization proof")
		}
		candidate := worst + successor.RemainingExposureSum
		best := chosenWorst + certificate.Optimization[certificate.Optimization[state.key()].SuccessorStateKey].RemainingExposureSum
		if candidate < best || (candidate == best && indexed.problem.Actions[actionIndex].ID < chosenID) {
			return fmt.Errorf("optimization choice is not lexicographically optimal")
		}
	}
	return nil
}

func verifyInfeasibleTransitions(indexed *indexedProblem, state *auditState, legal []int, entries map[string]FeasibilityCertificateEntry, cap int) error {
	for _, actionIndex := range legal {
		next := indexed.applyAction(state, actionIndex)
		if !indexed.validatorAccepts(next) {
			continue
		}
		exposures := indexed.exposureAt(next)
		worst, _ := indexed.worstExposure(exposures)
		if worst > cap {
			continue
		}
		entry, ok := entries[next.key()]
		if !ok || entry.Feasible {
			return fmt.Errorf("infeasible state has an unpruned feasible successor")
		}
	}
	return nil
}

func verifyPostAction(indexed *indexedProblem, state *auditState, cap int) error {
	if !indexed.validatorAccepts(state) {
		return fmt.Errorf("transition leaves no accepted validator digest")
	}
	exposures := indexed.exposureAt(state)
	worst, _ := indexed.worstExposure(exposures)
	if worst > cap {
		return fmt.Errorf("transition exposure %d exceeds cap %d", worst, cap)
	}
	return nil
}

func sameActions(indexed *indexedProblem, actual []int, ids []int) bool {
	actualIDs := make([]int, len(actual))
	for index, actionIndex := range actual {
		actualIDs[index] = indexed.problem.Actions[actionIndex].ID
	}
	sort.Ints(actualIDs)
	sort.Ints(ids)
	if len(actualIDs) != len(ids) {
		return false
	}
	for index := range actualIDs {
		if actualIDs[index] != ids[index] {
			return false
		}
	}
	return true
}

func findActionByID(indexed *indexedProblem, id int) int {
	for index, action := range indexed.problem.Actions {
		if action.ID == id {
			return index
		}
	}
	return -1
}

func containsAction(actions []int, target int) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}
