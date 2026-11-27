package eviction

import (
	"testing"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/estimator"
)

var baseTime = time.Unix(1_700_000_000, 0)

func memoryRec(target, lower, upper float64) ResourceRecommendation {
	return ResourceRecommendation{
		estimator.ResourceMemory: {
			LowerBound:     lower,
			Target:         target,
			UncappedTarget: target,
			UpperBound:     upper,
		},
	}
}

func runningPod(name string, memoryRequest float64) *Pod {
	return &Pod{
		Name:      name,
		Namespace: "default",
		Running:   true,
		Request:   Request{estimator.ResourceMemory: memoryRequest},
	}
}

func TestRequestOutsideBoundsIsEligible(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: 10 * time.Minute})
	pod := runningPod("p1", 50)

	decision := manager.Evaluate(pod, memoryRec(100, 90, 110), baseTime)
	if !decision.Eligible || decision.Reason != ReasonNone {
		t.Fatalf("expected eligible, got %+v", decision)
	}

	inside := runningPod("p2", 95)
	decision = manager.Evaluate(inside, memoryRec(100, 90, 110), baseTime)
	if decision.Eligible || decision.Reason != ReasonWithinBounds {
		t.Fatalf("expected within-bounds rejection, got %+v", decision)
	}
}

func TestMinChangeRatioTriggersInsideBounds(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: time.Minute})
	// Request 60 vs target 100: inside [55, 145] but 40% off — below the 50%
	// threshold, so not eligible.
	small := runningPod("small", 60)
	if decision := manager.Evaluate(small, memoryRec(100, 55, 145), baseTime); decision.Eligible {
		t.Fatalf("40%% deviation must not cross 50%% threshold: %+v", decision)
	}
	// Request 45 vs target 100: 55% off, threshold crossed.
	large := runningPod("large", 45)
	if decision := manager.Evaluate(large, memoryRec(100, 55, 145), baseTime); !decision.Eligible {
		t.Fatalf("55%% deviation must trigger eviction: %+v", decision)
	}
}

func TestCooldownBlocksEviction(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: 10 * time.Minute})
	pod := runningPod("p1", 50)
	pod.LastEvictionAt = baseTime.Add(-5 * time.Minute)

	decision := manager.Evaluate(pod, memoryRec(100, 90, 110), baseTime)
	if decision.Eligible || decision.Reason != ReasonCooldown {
		t.Fatalf("expected cooldown rejection, got %+v", decision)
	}

	// After the cooldown elapses the same pod becomes eligible.
	decision = manager.Evaluate(pod, memoryRec(100, 90, 110), baseTime.Add(6*time.Minute))
	if !decision.Eligible {
		t.Fatalf("expected eligibility after cooldown, got %+v", decision)
	}
}

func TestMinReplicasFloor(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 2, MinChangeRatio: 0.5, Cooldown: time.Minute})
	pods := []*Pod{runningPod("p1", 10), runningPod("p2", 20)}

	evicted, rejected := manager.EvictRound(pods, memoryRec(100, 90, 110), baseTime)
	if evicted != nil {
		t.Fatalf("no eviction allowed below minReplicas, evicted %+v", evicted)
	}
	found := false
	for _, decision := range rejected {
		if decision.Reason == ReasonMinReplicas {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected minReplicas rejection among %+v", rejected)
	}
}

func TestPDBBudgetConsumed(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: time.Minute})
	budget := &PodDisruptionBudget{
		Name:               "pdb",
		Namespace:          "default",
		SelectorPods:       map[string]bool{"p1": true, "p2": true},
		DisruptionsAllowed: 1,
	}
	manager.UpsertPDB(budget)

	pods := []*Pod{runningPod("p1", 10), runningPod("p2", 20)}
	evicted, _ := manager.EvictRound(pods, memoryRec(100, 90, 110), baseTime)
	if evicted == nil || evicted.Pod.Name != "p1" {
		t.Fatalf("expected most deviant p1 evicted, got %+v", evicted)
	}
	if budget.DisruptionsAllowed != 0 {
		t.Fatalf("budget not consumed: %d", budget.DisruptionsAllowed)
	}

	// Second round: PDB exhausted, nothing can be evicted despite eligibility.
	evicted, rejected := manager.EvictRound(pods, memoryRec(100, 90, 110), baseTime.Add(2*time.Minute))
	if evicted != nil {
		t.Fatalf("expected no eviction with exhausted PDB")
	}
	if len(rejected) == 0 || rejected[0].Reason != ReasonNoPDBBudget {
		t.Fatalf("expected PDB rejection, got %+v", rejected)
	}
}

func TestOnlyMostDeviantEvictedPerRound(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: time.Minute})
	manager.UpsertPDB(&PodDisruptionBudget{
		Name: "pdb", Namespace: "default",
		SelectorPods:       map[string]bool{"p1": true, "p2": true, "p3": true},
		DisruptionsAllowed: 10,
	})
	pods := []*Pod{
		runningPod("p1", 95), // inside [90,110], 5% off -> not eligible
		runningPod("p2", 20), // outside bounds, 80% off
		runningPod("p3", 30), // outside bounds, 70% off
	}

	evicted, rejected := manager.EvictRound(pods, memoryRec(100, 90, 110), baseTime)
	if evicted == nil || evicted.Pod.Name != "p2" {
		t.Fatalf("expected p2 (most deviant) evicted, got %+v", evicted)
	}
	roundLimit := 0
	for _, decision := range rejected {
		if decision.Reason == ReasonRoundLimit {
			roundLimit++
		}
	}
	if roundLimit != 1 {
		t.Fatalf("expected one round-limit skip, got %+v", rejected)
	}

	// The evicted pod now carries a fresh cooldown timestamp.
	if !pods[1].LastEvictionAt.Equal(baseTime) {
		t.Fatalf("cooldown timestamp not recorded: %v", pods[1].LastEvictionAt)
	}
}

func TestPDBSelectorDoesNotMatchOtherPods(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: time.Minute})
	manager.UpsertPDB(&PodDisruptionBudget{
		Name:               "pdb",
		Namespace:          "default",
		SelectorPods:       map[string]bool{"other": true},
		DisruptionsAllowed: 0,
	})
	pod := runningPod("unrelated", 10)
	sibling := runningPod("sibling", 100)

	// No matching PDB means no budget constraint for this pod.
	evicted, _ := manager.EvictRound([]*Pod{pod, sibling}, memoryRec(100, 90, 110), baseTime)
	if evicted == nil {
		t.Fatal("pod outside the PDB selector should be evictable")
	}
}

func TestNotRunningPodRejected(t *testing.T) {
	manager := NewManager(Config{MinReplicas: 1, MinChangeRatio: 0.5, Cooldown: time.Minute})
	pod := runningPod("p1", 10)
	pod.Running = false

	decision := manager.Evaluate(pod, memoryRec(100, 90, 110), baseTime)
	if decision.Eligible || decision.Reason != ReasonNotRunning {
		t.Fatalf("expected not-running rejection, got %+v", decision)
	}
}
