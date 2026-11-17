package netsec

import (
	"fmt"
	"sort"
)

// FailureCode classifies why no safe update plan exists.
type FailureCode string

const (
	// FailureInitialUnsafe means the initial graph already contains a forbidden
	// reachability.
	FailureInitialUnsafe FailureCode = "initial_unsafe"
	// FailureTargetUnsafe means the requested target graph contains a forbidden
	// reachability.
	FailureTargetUnsafe FailureCode = "target_unsafe"
	// FailureNoSafePath means both endpoint graphs are safe, but no sequence of
	// valid one-edge flips connects them.
	FailureNoSafePath FailureCode = "no_safe_path"
)

// Failure explains an infeasible update.
type Failure struct {
	Code       FailureCode
	Message    string
	Violations []ForbiddenPair
}

// Step is one atomic edge flip. From and To are the complete mutable-edge
// states before and after the action.
type Step struct {
	EdgeID string
	From   State
	To     State
}

// StateEvidence records one state examined by the exhaustive search.
type StateEvidence struct {
	Mask         State
	Distance     int
	EnabledEdges []string
	Violations   []ForbiddenPair
}

// Evidence is returned when planning fails. Traversed contains every safe
// state reachable from the initial state, in BFS discovery order. Blocked
// contains invalid states encountered at the search boundary, sorted by mask.
// Complete is true when this evidence covers the full reachable component.
type Evidence struct {
	TotalStates int
	Traversed   []StateEvidence
	Blocked     []StateEvidence
	Complete    bool
}

// PlanResult is the result of Plan.
type PlanResult struct {
	Feasible bool
	Distance int
	Steps    []Step
	Failure  *Failure
	Evidence Evidence
}

// EdgeIDs returns the action sequence as edge IDs.
func (r PlanResult) EdgeIDs() []string {
	ids := make([]string, len(r.Steps))
	for i, step := range r.Steps {
		ids[i] = step.EdgeID
	}
	return ids
}

type stateValidity struct {
	valid      bool
	violations []ForbiddenPair
}

// Plan computes a shortest sequence of one-edge flips from the request's
// initial state to its target state while keeping every state safe. Among all
// shortest sequences, it returns the lexicographically smallest sequence of
// edge IDs. It never mutates the request or contacts a live network.
func Plan(req Request) (PlanResult, error) {
	model, err := newNormalizedModel(req)
	if err != nil {
		return PlanResult{}, err
	}

	stateCount := model.stateCount()
	validity := make([]stateValidity, stateCount)
	for raw := 0; raw < stateCount; raw++ {
		state := State(raw)
		violations := model.violations(state)
		validity[raw] = stateValidity{
			valid:      len(violations) == 0,
			violations: violations,
		}
	}

	result := PlanResult{
		Evidence: Evidence{
			TotalStates: stateCount,
		},
	}

	initialValidity := validity[model.initial]
	if !initialValidity.valid {
		result.Failure = &Failure{
			Code:       FailureInitialUnsafe,
			Message:    "the initial graph contains a forbidden reachability",
			Violations: clonePairs(initialValidity.violations),
		}
		result.Evidence.Blocked = []StateEvidence{
			model.stateEvidence(model.initial, 0, initialValidity.violations),
		}
		result.Evidence.Complete = true
		return result, nil
	}

	distances, discovered, blocked := explore(model, validity)

	targetValidity := validity[model.target]
	if !targetValidity.valid {
		blocked = addBlockedState(blocked, model.stateEvidence(model.target, -1, targetValidity.violations))
		result.Failure = &Failure{
			Code:       FailureTargetUnsafe,
			Message:    "the target graph contains a forbidden reachability",
			Violations: clonePairs(targetValidity.violations),
		}
		result.Evidence.Traversed = stateEvidenceList(model, discovered, distances)
		result.Evidence.Blocked = sortEvidence(blocked)
		result.Evidence.Complete = true
		return result, nil
	}

	if distances[model.target] < 0 {
		result.Failure = &Failure{
			Code:    FailureNoSafePath,
			Message: "no safe sequence of one-edge flips reaches the target graph",
		}
		result.Evidence.Traversed = stateEvidenceList(model, discovered, distances)
		result.Evidence.Blocked = sortEvidence(blocked)
		result.Evidence.Complete = true
		return result, nil
	}

	toTarget := distancesToTarget(model, validity)
	steps := shortestLexicographicPath(model, validity, toTarget)
	if len(steps) != distances[model.target] {
		return PlanResult{}, fmt.Errorf("netsec: internal error reconstructing shortest path")
	}

	result.Feasible = true
	result.Distance = len(steps)
	result.Steps = steps
	return result, nil
}

func explore(model *normalizedModel, validity []stateValidity) ([]int, []State, []StateEvidence) {
	distances := make([]int, len(validity))
	for i := range distances {
		distances[i] = -1
	}
	distances[model.initial] = 0
	queue := []State{model.initial}
	discovered := []State{model.initial}
	blocked := make([]StateEvidence, 0)
	blockedSeen := make(map[State]struct{})

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for index := range model.edges {
			next := current ^ (State(1) << uint(index))
			if !validity[next].valid {
				if _, exists := blockedSeen[next]; !exists {
					blockedSeen[next] = struct{}{}
					blocked = append(
						blocked,
						model.stateEvidence(next, distances[current]+1, validity[next].violations),
					)
				}
				continue
			}
			if distances[next] >= 0 {
				continue
			}
			distances[next] = distances[current] + 1
			queue = append(queue, next)
			discovered = append(discovered, next)
		}
	}

	return distances, discovered, blocked
}

func distancesToTarget(model *normalizedModel, validity []stateValidity) []int {
	distances := make([]int, len(validity))
	for i := range distances {
		distances[i] = -1
	}
	distances[model.target] = 0
	queue := []State{model.target}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for index := range model.edges {
			next := current ^ (State(1) << uint(index))
			if !validity[next].valid || distances[next] >= 0 {
				continue
			}
			distances[next] = distances[current] + 1
			queue = append(queue, next)
		}
	}
	return distances
}

func shortestLexicographicPath(
	model *normalizedModel,
	validity []stateValidity,
	toTarget []int,
) []Step {
	remaining := toTarget[model.initial]
	steps := make([]Step, 0, remaining)
	current := model.initial

	for remaining > 0 {
		advanced := false
		for index, edge := range model.edges {
			next := current ^ (State(1) << uint(index))
			if !validity[next].valid || toTarget[next] != remaining-1 {
				continue
			}
			steps = append(steps, Step{
				EdgeID: edge.ID,
				From:   current,
				To:     next,
			})
			current = next
			remaining--
			advanced = true
			break
		}
		if !advanced {
			return nil
		}
	}
	return steps
}

func (m *normalizedModel) stateEvidence(
	state State,
	distance int,
	violations []ForbiddenPair,
) StateEvidence {
	return StateEvidence{
		Mask:         state,
		Distance:     distance,
		EnabledEdges: m.enabledEdgeIDs(state),
		Violations:   clonePairs(violations),
	}
}

func stateEvidenceList(model *normalizedModel, states []State, distances []int) []StateEvidence {
	evidence := make([]StateEvidence, 0, len(states))
	for _, state := range states {
		evidence = append(evidence, model.stateEvidence(state, distances[state], nil))
	}
	return evidence
}

func addBlockedState(blocked []StateEvidence, evidence StateEvidence) []StateEvidence {
	for i, existing := range blocked {
		if existing.Mask == evidence.Mask {
			if existing.Distance < 0 && evidence.Distance >= 0 {
				blocked[i].Distance = evidence.Distance
			}
			return blocked
		}
	}
	return append(blocked, evidence)
}

func sortEvidence(evidence []StateEvidence) []StateEvidence {
	sort.Slice(evidence, func(i, j int) bool {
		return evidence[i].Mask < evidence[j].Mask
	})
	return evidence
}

func clonePairs(pairs []ForbiddenPair) []ForbiddenPair {
	if len(pairs) == 0 {
		return nil
	}
	return append([]ForbiddenPair(nil), pairs...)
}

// Verify checks a successful plan against the request and recomputes the
// shortest distance with an exhaustive BFS. It also checks that the returned
// action sequence is lexicographically minimal among shortest plans.
func Verify(req Request, plan PlanResult) error {
	if !plan.Feasible {
		return fmt.Errorf("netsec: cannot verify an infeasible plan")
	}
	model, err := newNormalizedModel(req)
	if err != nil {
		return err
	}
	if len(plan.Steps) != plan.Distance {
		return fmt.Errorf(
			"netsec: distance %d does not match %d steps",
			plan.Distance, len(plan.Steps),
		)
	}

	validity := make([]stateValidity, model.stateCount())
	for raw := range validity {
		violations := model.violations(State(raw))
		validity[raw] = stateValidity{valid: len(violations) == 0, violations: violations}
	}
	if !validity[model.initial].valid {
		return fmt.Errorf("netsec: initial state is unsafe")
	}
	if !validity[model.target].valid {
		return fmt.Errorf("netsec: target state is unsafe")
	}

	current := model.initial
	for i, step := range plan.Steps {
		index := model.edgeIndex(step.EdgeID)
		if index < 0 {
			return fmt.Errorf("netsec: step %d references unknown edge %q", i, step.EdgeID)
		}
		next := current ^ (State(1) << uint(index))
		if step.From != current || step.To != next {
			return fmt.Errorf("netsec: step %d does not match the in-memory model", i)
		}
		if !validity[next].valid {
			return fmt.Errorf("netsec: step %d reaches an unsafe state", i)
		}
		current = next
	}
	if current != model.target {
		return fmt.Errorf("netsec: plan ends at state %d, want %d", current, model.target)
	}

	distances, _, _ := explore(model, validity)
	shortest := distances[model.target]
	if shortest != plan.Distance {
		return fmt.Errorf(
			"netsec: plan length %d is not the shortest distance %d",
			plan.Distance, shortest,
		)
	}

	toTarget := distancesToTarget(model, validity)
	expected := shortestLexicographicPath(model, validity, toTarget)
	if len(expected) != plan.Distance {
		return fmt.Errorf("netsec: cannot reconstruct the expected shortest path")
	}
	got := plan.EdgeIDs()
	for i := range expected {
		if got[i] != expected[i].EdgeID {
			return fmt.Errorf(
				"netsec: action sequence is not lexicographically minimal at step %d",
				i,
			)
		}
	}
	return nil
}

func (m *normalizedModel) edgeIndex(id string) int {
	for i, edge := range m.edges {
		if edge.ID == id {
			return i
		}
	}
	return -1
}
