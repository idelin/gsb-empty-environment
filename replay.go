package upgradeplan

import (
	"fmt"
	"slices"
)

// ReplayStep is one host-supplied broadcast and optional device-choice vector.
// Assignment indexes NewSignerSets for each eligible device in canonical order.
type ReplayStep struct {
	ReleaseID  string `json:"release_id"`
	Assignment []int  `json:"assignment,omitempty"`
}

// ReplayTrajectory replays a concrete broadcast/choice trace against verified
// records. It derives sticky data-loss state itself; callers do not need to set
// InitialData or DataLost manually. The returned trace is populated even when
// acceptance fails.
func ReplayTrajectory(problem Problem, initial []DeviceState, steps []ReplayStep) (*Trajectory, error) {
	np, err := NormalizeProblem(problem)
	if err != nil {
		return nil, err
	}

	fleet := CanonicalFleet(initial)
	trace := &Trajectory{Initial: append(Fleet(nil), fleet...)}

	for stepIndex, input := range steps {
		release, ok := np.byID[input.ReleaseID]
		if !ok {
			trace.Final = append(Fleet(nil), fleet...)
			return trace, fmt.Errorf("upgradeplan: step %d: unknown release %q", stepIndex+1, input.ReleaseID)
		}

		outcomes, err := EnumerateOutcomes(fleet, release)
		if err != nil {
			trace.Final = append(Fleet(nil), fleet...)
			return trace, err
		}
		chosen, err := chooseOutcome(outcomes, input.Assignment)
		if err != nil {
			trace.Final = append(Fleet(nil), fleet...)
			return trace, fmt.Errorf("upgradeplan: step %d release %q: %w", stepIndex+1, release.ID, err)
		}

		traceStep := TrajectoryStep{
			Round:        stepIndex + 1,
			Broadcast:    Broadcast{ReleaseID: release.ID, Package: release.Package},
			SuccessCount: chosen.SuccessCount,
			Eligible:     append([]int(nil), chosen.Eligible...),
			Assignment:   append([]int(nil), chosen.Assignment...),
			ChosenSets:   append([]SignerSet(nil), chosen.ChosenSets...),
			Before:       append(Fleet(nil), fleet...),
			After:        append(Fleet(nil), chosen.Fleet...),
		}
		trace.Steps = append(trace.Steps, traceStep)
		fleet = chosen.Fleet
	}

	trace.Final = append(Fleet(nil), fleet...)
	if err := AcceptTrajectory(*trace, problem.Target); err != nil {
		return trace, err
	}
	return trace, nil
}

func chooseOutcome(outcomes []Outcome, assignment []int) (*Outcome, error) {
	if len(assignment) == 0 {
		if len(outcomes) == 1 {
			return &outcomes[0], nil
		}
		return nil, fmt.Errorf("assignment is required because %d device-choice outcomes are possible", len(outcomes))
	}
	for i := range outcomes {
		if slices.Equal(outcomes[i].Assignment, assignment) {
			return &outcomes[i], nil
		}
	}
	return nil, fmt.Errorf("no outcome matches assignment %v", assignment)
}
