package repairplan

import (
	"errors"
	"testing"
	"time"
)

// TestUnrecoverableAndHealthy verifies stripes under k survivors are
// classified UNRECOVERABLE and never scheduled, while fully-present stripes
// need no repair.
func TestUnrecoverableAndHealthy(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{
		offline: map[string]bool{osdName(3, 1, 1): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// k=3 needs three survivors but only two placements exist; the third
	// surviving shard's OSD is also offline so surviving stays below k.
	dead := Stripe{
		ID: "dead", K: 3, M: 2, ShardSize: 100, FaultDomain: LevelRack,
		Shards: []ShardPlacement{
			{0, osdName(1, 1, 1)},
			{1, osdName(2, 1, 1)},
			{2, osdName(3, 1, 1)}, // offline -> not surviving
		},
	}
	healthy := Stripe{
		ID: "fine", K: 2, M: 1, ShardSize: 100, FaultDomain: LevelRack,
		Shards: []ShardPlacement{
			{0, osdName(1, 1, 2)},
			{1, osdName(2, 1, 2)},
			{2, osdName(3, 1, 2)},
		},
	}
	plan := mustPlan(t, topo, []Stripe{dead, healthy}, Options{})

	if len(plan.Unrecoverable) != 1 || plan.Unrecoverable[0].StripeID != "dead" {
		t.Fatalf("expected dead to be unrecoverable, got %+v", plan.Unrecoverable)
	}
	if plan.Unrecoverable[0].Surviving != 2 || plan.Unrecoverable[0].Status != StatusUnrecoverable {
		t.Fatalf("unexpected classification %+v", plan.Unrecoverable[0])
	}
	deadReport := report(plan, "dead")
	if deadReport.Scheduled || len(deadReport.Tasks) != 0 {
		t.Fatalf("unrecoverable stripe must never be scheduled")
	}
	fine := report(plan, "fine")
	if fine.Status != StatusHealthy || fine.Scheduled || len(fine.Tasks) != 0 {
		t.Fatalf("healthy stripe should need no repair, got %+v", fine)
	}
	if plan.TotalRounds != 0 {
		t.Fatalf("expected no rounds when nothing is repairable, got %d", plan.TotalRounds)
	}
}

// TestGlobalConcurrentSlots verifies the global repair slot cap queues tasks.
func TestGlobalConcurrentSlots(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{})
	if err != nil {
		t.Fatal(err)
	}
	stripes := []Stripe{
		NewReplicatedStripe("pg-a", 3, 100, LevelRack, []ShardPlacement{
			{0, osdName(1, 1, 1)}, {1, osdName(2, 1, 1)},
		}),
		NewReplicatedStripe("pg-b", 3, 100, LevelRack, []ShardPlacement{
			{0, osdName(1, 1, 2)}, {1, osdName(2, 1, 2)},
		}),
		NewReplicatedStripe("pg-c", 3, 100, LevelRack, []ShardPlacement{
			{0, osdName(1, 2, 1)}, {1, osdName(2, 2, 1)},
		}),
	}
	plan := mustPlan(t, topo, stripes, Options{GlobalConcurrent: 2, RoundDuration: time.Second})
	if plan.TotalRounds != 2 {
		t.Fatalf("expected 2 rounds for 3 tasks with 2 slots, got %d", plan.TotalRounds)
	}
	if len(plan.Rounds[0].Tasks) != 2 || len(plan.Rounds[1].Tasks) != 1 {
		t.Fatalf("unexpected task distribution: %d / %d",
			len(plan.Rounds[0].Tasks), len(plan.Rounds[1].Tasks))
	}
	if got := report(plan, "pg-c").CompletedRound; got != 2 {
		t.Fatalf("pg-c should finish in round 2, got %d", got)
	}
}

// TestNodeConcurrency verifies per-node MaxConcurrent is enforced.
func TestNodeConcurrency(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{rackMaxConcur: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Both stripes read a survivor out of rack1; rack1 concurrency cap is 1.
	s1 := NewReplicatedStripe("nc-1", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)}, {1, osdName(2, 1, 1)},
	})
	s2 := NewReplicatedStripe("nc-2", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 2, 1)}, {1, osdName(2, 2, 1)},
	})
	plan := mustPlan(t, topo, []Stripe{s1, s2}, Options{GlobalConcurrent: 10})
	if plan.TotalRounds != 2 {
		t.Fatalf("expected rack concurrency cap to force 2 rounds, got %d", plan.TotalRounds)
	}
	for _, round := range plan.Rounds {
		if usage := round.NodeUsage[rackName(1)]; usage.ConcurrentTasks > 1 {
			t.Fatalf("rack1 concurrency %d exceeded cap in round %d",
				usage.ConcurrentTasks, round.Round)
		}
	}
}

// TestNoProgress returns ErrNoProgress when a task can never fit.
func TestNoProgress(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{
		rackCount:     3,
		perRackEgress: map[string]int64{rackName(1): 10}, // below the 100-byte read
	})
	if err != nil {
		t.Fatal(err)
	}
	stripe := NewReplicatedStripe("stuck", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)}, {1, osdName(2, 1, 1)},
	})
	_, err = BuildPlan(topo, []Stripe{stripe}, Options{})
	if !errors.Is(err, ErrNoProgress) {
		t.Fatalf("expected ErrNoProgress, got %v", err)
	}
}

// TestIngressCountedOnce verifies a multi-source rebuild charges the target
// ingress nodes once per task, not once per source.
func TestIngressCountedOnce(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{rackCount: 4})
	if err != nil {
		t.Fatal(err)
	}
	stripe := Stripe{
		ID: "ingress-once", K: 3, M: 2, ShardSize: 100, FaultDomain: LevelHost,
		Shards: []ShardPlacement{
			{0, osdName(1, 1, 1)},
			{1, osdName(2, 1, 1)},
			{2, osdName(3, 1, 1)},
			// two missing shards; targets land on rack4's two hosts
		},
	}
	plan := mustPlan(t, topo, []Stripe{stripe}, Options{})
	if plan.TotalRounds != 1 {
		t.Fatalf("expected both rebuilds in one round, got %d", plan.TotalRounds)
	}
	round := plan.Rounds[0]
	if len(round.Tasks) != 2 {
		t.Fatalf("expected two rebuild tasks, got %d", len(round.Tasks))
	}
	// Each target OSD receives exactly one shard write even though the
	// rebuild pulls from k=3 sources.
	wantRackEgress := map[string]int64{}
	for _, task := range round.Tasks {
		usage := round.NodeUsage[task.TargetOSD]
		if usage.IngressBytes != 100 {
			t.Fatalf("target %s ingress %d should be 100 (charged once, not per source)",
				task.TargetOSD, usage.IngressBytes)
		}
		targetRack := topo.RackOf(task.TargetOSD)
		if got := round.NodeUsage[targetRack].IngressBytes; got != 100 {
			t.Fatalf("target rack %s ingress %d should be 100 per task", targetRack, got)
		}
		for _, src := range task.SourceOSDs {
			if topo.RackOf(src) != targetRack {
				wantRackEgress[topo.RackOf(src)] += 100
			}
		}
	}
	// A source rack only pays egress for reads leaving toward another rack.
	for rack, want := range wantRackEgress {
		if got := round.NodeUsage[rack].EgressBytes; got != want {
			t.Fatalf("rack %s egress %d should be %d", rack, got, want)
		}
	}
}

// TestInvalidInputs covers structural validation errors.
func TestInvalidInputs(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPlan(nil, []Stripe{{ID: "x"}}, Options{}); err == nil {
		t.Fatal("expected error for nil topology")
	}
	bad := Stripe{ID: "bad", K: 0, M: 2, ShardSize: 10, FaultDomain: LevelRack,
		Shards: []ShardPlacement{{0, "x"}}}
	if _, err := BuildPlan(topo, []Stripe{bad}, Options{}); err == nil {
		t.Fatal("expected error for non-positive k")
	}
	unknown := NewReplicatedStripe("unk", 3, 100, LevelRack, []ShardPlacement{
		{0, "ghost-osd"}, {1, osdName(2, 1, 1)},
	})
	if _, err := BuildPlan(topo, []Stripe{unknown}, Options{}); err == nil {
		t.Fatal("expected error for unknown OSD reference")
	}
}

// TestTopologyValidation rejects malformed trees.
func TestTopologyValidation(t *testing.T) {
	if _, err := NewTopology(nil); err == nil {
		t.Fatal("expected error for empty topology")
	}
	twoRoots := []Node{
		{ID: "r1", Level: LevelRoot, Online: true},
		{ID: "r2", Level: LevelRoot, Online: true},
	}
	if _, err := NewTopology(twoRoots); err == nil {
		t.Fatal("expected error for two roots")
	}
	badParent := []Node{
		{ID: "root", Level: LevelRoot, Online: true},
		{ID: "rack1", Level: LevelRack, ParentID: "root", Online: true},
	}
	if _, err := NewTopology(badParent); err == nil {
		t.Fatal("expected error for level-skipping parent link")
	}
}
