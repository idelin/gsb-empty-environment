package updatestrategy_test

import (
	"errors"
	"fmt"

	updatestrategy "prescreen.local/ddd-10525-v2"
)

// ExamplePlan shows the importable entry point: declare the worlds compatible
// with the initial public readback, the host action set, and the signed-image
// target; receive the minimum worst-case observation policy.
func ExamplePlan() {
	const (
		oldImg updatestrategy.Digest = "signed-old"
		newImg updatestrategy.Digest = "signed-new"
	)
	initial := updatestrategy.World{
		Active:  updatestrategy.SlotA,
		Slots:   [2]updatestrategy.Digest{oldImg, updatestrategy.EmptyDigest},
		Pending: updatestrategy.SlotA,
	}
	torn := initial
	torn.PowerLost = true
	torn.Slots[updatestrategy.SlotB] = updatestrategy.UnknownDigest

	actions := []updatestrategy.Action{
		updatestrategy.WriteAction{Target: updatestrategy.SlotB, Image: newImg, Acknowledge: true},
		updatestrategy.SetPendingAction{Target: updatestrategy.SlotB},
		updatestrategy.BurnCounterAction{Amount: 1, Floor: 1},
		updatestrategy.RebootAction{},
		updatestrategy.ConfirmAction{},
	}

	policy, err := updatestrategy.Plan(updatestrategy.Problem{
		InitialBelief: []updatestrategy.World{initial, torn},
		Actions:       actions,
		Target:        updatestrategy.Target{TargetImage: newImg, MinCounter: 1},
	})
	if err != nil {
		var refusal *updatestrategy.Refusal
		if errors.As(err, &refusal) {
			fmt.Println("refused:", refusal.Reason)
		}
		return
	}
	if violations := updatestrategy.VerifyPolicy(policy, actions); len(violations) != 0 {
		fmt.Println("invariant violation:", violations[0])
		return
	}

	runs, err := updatestrategy.ExhaustiveRun(policy, torn)
	if err != nil {
		fmt.Println("simulation error:", err)
		return
	}
	fmt.Printf("planned worst ops: %d, enumerated paths: %d, all reach target: %t\n",
		policy.WorstOps, len(runs), allReach(policy, runs))
	// Output: planned worst ops: 6, enumerated paths: 1, all reach target: true
}

func allReach(p *updatestrategy.Policy, runs []updatestrategy.RunResult) bool {
	for _, r := range runs {
		if !r.ReachedGoal {
			return false
		}
	}
	return p != nil && len(runs) > 0
}
