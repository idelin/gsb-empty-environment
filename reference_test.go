package updatestrategy

import (
	"errors"
	"math/rand"
	"testing"
)

// The tiny reference world: two physical slots, one signing root (the host
// already rejects foreign signatures before anything enters these tests), an
// installed factory image Old and the requested update New.
const (
	Old Digest = "digest-old"
	New Digest = "digest-new"
)

func factoryWorld(powerLost bool, slotB Digest) World {
	return World{
		Active:    SlotA,
		Slots:     [2]Digest{Old, slotB},
		Pending:   SlotA,
		Counter:   0,
		PowerLost: powerLost,
	}
}

// fullHostActions is the action set available to a normal update host.
func fullHostActions() []Action {
	return []Action{
		WriteAction{Target: SlotB, Image: New, Acknowledge: true},
		WriteAction{Target: SlotB, Image: New, Acknowledge: false},
		SetPendingAction{Target: SlotB},
		BurnCounterAction{Amount: 2, Floor: 1},
		BurnCounterAction{Amount: 1, Floor: 1},
		RebootAction{},
		ConfirmAction{},
	}
}

func TestS1_WriteUnknownBeforeSwitchAndWorstCost(t *testing.T) {
	// Scenario 1: after a reboot the standby write cannot be read back. The
	// initial belief spans every persistent result the interrupted write may
	// have produced, including an unknown unauthenticated candidate.
	belief := []World{
		factoryWorld(true, New),           // write actually landed
		factoryWorld(true, EmptyDigest),   // nothing durable happened
		factoryWorld(true, UnknownDigest), // torn write: unknown candidate
	}
	policy, err := Plan(Problem{
		InitialBelief: belief,
		Actions:       fullHostActions(),
		Target:        Target{TargetImage: New, MinCounter: 1},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if errs := VerifyPolicy(policy, fullHostActions()); len(errs) != 0 {
		t.Fatalf("invariants: %v", errs)
	}
	for _, start := range belief {
		runs, err := ExhaustiveRun(policy, start)
		if err != nil {
			t.Fatalf("exhaustive run from %s: %v", start.ID(), err)
		}
		if got := WorstObserved(runs); got > policy.WorstOps {
			t.Fatalf("observed worst %d exceeds planned %d", got, policy.WorstOps)
		}
		for _, r := range runs {
			if !r.ReachedGoal || r.PowerLosses > 1 {
				t.Fatalf("bad run: %+v", r)
			}
			if !r.Final.Bootable(r.Final.Active) {
				t.Fatalf("final world not bootable: %s", r.Final.ID())
			}
		}
	}
	// Hand-derived optimum for this reference: write, set pending, burn the
	// counter, reboot readback = four worst-case operations. A separate
	// confirm is unnecessary because the reboot lands directly on the
	// authenticated target with the pointer already committed.
	if policy.WorstOps != 4 {
		t.Fatalf("worst operations = %d, want 4", policy.WorstOps)
	}
}

func TestS2_EarlyCounterBurnRemovesLastRecoveryPath(t *testing.T) {
	// Scenario 2: a counter burn is attempted before the slot switch. Its
	// atomic commit either landed or was lost in the interruption, and the
	// device cannot read the counter back before the next reboot. The two
	// hidden worlds therefore share one public observation history, yet one
	// already sits at the counter floor while the other has not advanced. The
	// only remaining burn action is a fixed +2: applying it overshoots the
	// high world past the floor (no recovery action can lower the counter
	// again), while skipping it strands the low world below the floor. No
	// uniform observation-based action exists, so planning must refuse.
	low := factoryWorld(true, EmptyDigest) // burn was lost
	high := factoryWorld(true, EmptyDigest)
	high.Counter = 2 // burn landed; already at the required floor
	actions := []Action{
		WriteAction{Target: SlotB, Image: New, Acknowledge: true},
		SetPendingAction{Target: SlotB},
		BurnCounterAction{Amount: 2, Floor: 2}, // fixed-size commit is the only counter op
		ConfirmAction{},
	}
	_, err := Plan(Problem{
		InitialBelief: []World{high, low},
		Actions:       actions,
		Target:        Target{TargetImage: New, MinCounter: 2},
	})
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("want ErrPlanningRefused/Refusal, got %v", err)
	}
	if refusal.Reason != RefusalDivergentBelief {
		t.Fatalf("reason = %s, want indistinguishable-divergent-action", refusal.Reason)
	}
	if len(refusal.FailedActions) == 0 {
		t.Fatal("refusal must name the actions that fail for hidden worlds")
	}
	if !errors.Is(err, ErrPlanningRefused) {
		t.Fatalf("refusal must wrap ErrPlanningRefused, got %v", err)
	}
}

func TestS3_SuccessAndPowerLossShareOneObservation(t *testing.T) {
	// Scenario 3: a host whose only write channel is unacknowledged. A clean
	// write completion and an interruption right after it produce the same
	// public observation, and no reboot readback action exists. The hidden
	// slot can therefore never be certified and the target is unreachable;
	// the planner must refuse instead of assuming the write landed.
	belief := []World{
		factoryWorld(false, EmptyDigest),
		factoryWorld(true, UnknownDigest),
	}
	actions := []Action{
		WriteAction{Target: SlotB, Image: New, Acknowledge: false},
		SetPendingAction{Target: SlotB},
		BurnCounterAction{Amount: 1, Floor: 1},
		ConfirmAction{},
	}
	_, err := Plan(Problem{
		InitialBelief: belief,
		Actions:       actions,
		Target:        Target{TargetImage: New, MinCounter: 1},
	})
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("want refusal, got %v", err)
	}
	if refusal.Reason != RefusalDivergentBelief && refusal.Reason != RefusalGoalUnreachable {
		t.Fatalf("reason = %s, want divergent or unreachable", refusal.Reason)
	}
}

func TestS4_RebootReadbackEnablesSafeBranching(t *testing.T) {
	// Scenario 4: one belief, three hidden worlds. Only the reboot readback
	// splits them by authenticated digest, after which each branch gets its own
	// recovery action. This proves the same WriteNoAck observation merging that
	// forced refusal in scenario 3 is safe once a readback channel exists.
	belief := []World{
		factoryWorld(false, EmptyDigest),
		factoryWorld(false, UnknownDigest),
		{
			Active:  SlotA,
			Slots:   [2]Digest{Old, New},
			Pending: SlotB,
			Counter: 0,
		},
	}
	policy, err := Plan(Problem{
		InitialBelief: belief,
		Actions:       fullHostActions(),
		Target:        Target{TargetImage: New, MinCounter: 1},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if errs := VerifyPolicy(policy, fullHostActions()); len(errs) != 0 {
		t.Fatalf("invariants: %v", errs)
	}

	// Model-level readback check: applying Reboot directly to the initial
	// ambiguous belief must partition it by authenticated digest. The New
	// world authenticates its pending slot; the empty/torn worlds fall back to
	// Old. This is the only channel that safely separates the hidden states.
	readbackSeen := false
	reboot := RebootAction{}
	rootGroups := Branch(policy.Root.Belief, reboot)
	readbackDigests := map[Digest]bool{}
	for _, g := range rootGroups {
		if g.Obs.Kind == ObsReboot {
			readbackDigests[g.Obs.ActiveDigest] = true
			readbackSeen = true
		}
	}
	if !readbackSeen || !readbackDigests[Old] || !readbackDigests[New] {
		t.Fatalf("reboot readback must split Old vs New, got %v", readbackDigests)
	}

	// Policy-level check: every reboot node inside the produced policy must use
	// the exact indistinguishable world set as its successor, never a smaller
	// set that would imply hidden slot knowledge.
	var walk func(n *PolicyNode)
	walk = func(n *PolicyNode) {
		if n == nil || n.IsGoal {
			return
		}
		if n.Action.Name() == reboot.Name() {
			for obs, child := range n.Branches {
				if obs.Kind != ObsReboot {
					continue
				}
				if child.Belief.Key() != MakeBelief(collectRebootWorlds(n.Belief, obs)).Key() {
					t.Fatalf("readback branch uses hidden knowledge or wrong world set")
				}
			}
		}
		for _, child := range n.Branches {
			walk(child)
		}
	}
	walk(policy.Root)

	for _, start := range belief {
		runs, err := ExhaustiveRun(policy, start)
		if err != nil {
			t.Fatalf("exhaustive run: %v", err)
		}
		for _, r := range runs {
			if !r.ReachedGoal {
				t.Fatalf("run missed target: %+v", r)
			}
		}
	}

	rng := rand.New(rand.NewSource(1))
	for _, start := range belief {
		rr, err := RunRandom(policy, start, 50, rng)
		if err != nil {
			t.Fatalf("random run: %v", err)
		}
		for _, r := range rr {
			if !r.ReachedGoal {
				t.Fatal("random run missed target")
			}
		}
	}
}

func TestWriteActionNeverTouchesAuthenticatedActive(t *testing.T) {
	w := factoryWorld(false, EmptyDigest)
	if err := (WriteAction{Target: SlotA, Image: New}).SafeIn(w); err == nil {
		t.Fatal("write to active slot must be rejected as unsafe")
	}
}

// collectRebootWorlds rebuilds the exact successor world set for one reboot
// observation straight from the transition model.
func collectRebootWorlds(b Belief, want Observation) []World {
	reboot := RebootAction{}
	var worlds []World
	for _, w := range b.worlds {
		for _, oc := range reboot.Apply(w) {
			if oc.Obs == want {
				worlds = append(worlds, oc.World)
			}
		}
	}
	return worlds
}
