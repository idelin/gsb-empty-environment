package repairplan

import (
	"testing"
	"time"
)

func report(plan *Plan, id string) StripeReport {
	for _, r := range plan.StripeReports {
		if r.StripeID == id {
			return r
		}
	}
	return StripeReport{}
}

func mustPlan(t *testing.T, topo *Topology, stripes []Stripe, opts Options) *Plan {
	t.Helper()
	plan, err := BuildPlan(topo, stripes, opts)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	return plan
}

// TestEndangeredFirst covers acceptance: surviving == k (margin 0) stripes
// are repaired before healthier ones, with size and ID tie-breaks.
func TestEndangeredFirst(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{rackCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	critical := NewReplicatedStripe("stripe-critical", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)},
		{1, osdName(2, 1, 1)},
	})
	// Separate rack domains so both stripes remain schedulable together
	// once the slot cap allows parallel execution.
	healthier := Stripe{
		ID: "stripe-healthier", K: 2, M: 3, ShardSize: 100, FaultDomain: LevelRack,
		Shards: []ShardPlacement{
			{0, osdName(1, 1, 2)},
			{1, osdName(2, 1, 2)},
			{2, osdName(3, 1, 1)},
			{3, osdName(4, 1, 1)},
			// shard 4 missing: four survivors, k=2 -> margin 2
		},
	}
	// Strictly healthier than the replicated stripe whose two survivors
	// equal k (margin 0).
	plan := mustPlan(t, topo, []Stripe{healthier, critical}, Options{GlobalConcurrent: 1})

	if plan.TotalRounds != 2 {
		t.Fatalf("expected 2 rounds under one global slot, got %d", plan.TotalRounds)
	}
	if got := plan.Rounds[0].Tasks[0].StripeID; got != "stripe-critical" {
		t.Fatalf("expected margin-0 stripe first, got %s", got)
	}
	if got := plan.Rounds[1].Tasks[0].StripeID; got != "stripe-healthier" {
		t.Fatalf("expected margin-1 stripe second, got %s", got)
	}

	big := NewReplicatedStripe("stripe-big", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 2, 1)}, {1, osdName(3, 2, 1)},
	})
	small := NewReplicatedStripe("stripe-small", 3, 50, LevelRack, []ShardPlacement{
		{0, osdName(2, 2, 1)}, {1, osdName(4, 2, 1)},
	})
	plan2 := mustPlan(t, topo, []Stripe{small, big}, Options{GlobalConcurrent: 1})
	if got := plan2.Rounds[0].Tasks[0].StripeID; got != "stripe-big" {
		t.Fatalf("expected larger stripe first on equal margin, got %s", got)
	}

	idA := NewReplicatedStripe("stripe-aaa", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)}, {1, osdName(3, 1, 1)},
	})
	idB := NewReplicatedStripe("stripe-bbb", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(2, 1, 1)}, {1, osdName(4, 1, 1)},
	})
	plan3 := mustPlan(t, topo, []Stripe{idB, idA}, Options{GlobalConcurrent: 1})
	if got := plan3.Rounds[0].Tasks[0].StripeID; got != "stripe-aaa" {
		t.Fatalf("expected lexicographic ID tie-break, got %s", got)
	}
}

// TestFaultDomainAntiAffinity verifies targets never share the mandatory
// fault-domain ancestor with surviving or already-planned shards.
func TestFaultDomainAntiAffinity(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{})
	if err != nil {
		t.Fatal(err)
	}
	stripe := Stripe{
		ID: "ec-rack", K: 2, M: 2, ShardSize: 100, FaultDomain: LevelRack,
		Shards: []ShardPlacement{
			{0, osdName(1, 1, 1)},
			{1, osdName(2, 1, 1)},
			{2, osdName(3, 1, 1)},
		},
	}
	plan := mustPlan(t, topo, []Stripe{stripe}, Options{})
	r := report(plan, "ec-rack")
	if !r.Scheduled || len(r.Tasks) != 1 {
		t.Fatalf("expected one scheduled task, got scheduled=%v tasks=%d failures=%v",
			r.Scheduled, len(r.Tasks), r.Failures)
	}
	target := r.Tasks[0].TargetOSD
	occupied := map[string]bool{}
	for _, sh := range stripe.Shards {
		occupied[topo.RackOf(sh.OSDID)] = true
	}
	if occupied[topo.RackOf(target)] {
		t.Fatalf("target %s rack collides with a surviving shard", target)
	}

	hostStripe := NewReplicatedStripe("host-aff", 3, 100, LevelHost, []ShardPlacement{
		{0, osdName(3, 1, 1)},
		{1, osdName(3, 2, 1)},
	})
	plan2 := mustPlan(t, topo, []Stripe{hostStripe}, Options{})
	r2 := report(plan2, "host-aff")
	if !r2.Scheduled || len(r2.Tasks) != 1 {
		t.Fatalf("expected host-affinity placement in another rack, got %+v", r2.Failures)
	}
	targetHost, _ := topo.Ancestor(r2.Tasks[0].TargetOSD, LevelHost)
	if targetHost.ID == hostName(3, 1) || targetHost.ID == hostName(3, 2) {
		t.Fatalf("host anti-affinity violated, target host %s", targetHost.ID)
	}
}

// TestDomainExhaustion reports a precise FAULT_DOMAIN_EXHAUSTED failure and
// never places a shard illegally.
func TestDomainExhaustion(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{
		rackCount: 3,
		offline:   map[string]bool{rackName(3): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	stripe := NewReplicatedStripe("dom-tight", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)},
		{1, osdName(2, 1, 1)},
	})
	plan := mustPlan(t, topo, []Stripe{stripe}, Options{})
	r := report(plan, "dom-tight")
	if r.Scheduled || len(r.Tasks) != 0 {
		t.Fatalf("expected blocked stripe with no tasks, got scheduled=%v tasks=%v",
			r.Scheduled, r.Tasks)
	}
	if len(r.Failures) != 1 || r.Failures[0].Reason != ReasonDomainExhausted {
		t.Fatalf("expected one FAULT_DOMAIN_EXHAUSTED, got %+v", r.Failures)
	}
	if r.Failures[0].FaultDomain != LevelRack {
		t.Fatalf("expected rack-level failure detail, got %s", r.Failures[0].FaultDomain)
	}
}

// TestCapacityExhausted distinguishes capacity failure from domain failure.
func TestCapacityExhausted(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{
		rackCount:   3,
		osdCapacity: 50, // every OSD is too small for a 100-byte shard
	})
	if err != nil {
		t.Fatal(err)
	}
	stripe := NewReplicatedStripe("cap-tight", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)},
		{1, osdName(2, 1, 1)},
	})
	plan := mustPlan(t, topo, []Stripe{stripe}, Options{})
	r := report(plan, "cap-tight")
	if r.Scheduled {
		t.Fatalf("expected capacity-blocked stripe")
	}
	if len(r.Failures) != 1 || r.Failures[0].Reason != ReasonCapacityExhausted {
		t.Fatalf("expected CAPACITY_EXHAUSTED, got %+v", r.Failures)
	}
}

// TestSourceSelection verifies exactly k sources are picked and same-rack
// survivors are preferred to minimize cross-rack read traffic.
func TestSourceSelection(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{})
	if err != nil {
		t.Fatal(err)
	}
	stripe := Stripe{
		ID: "src-pick", K: 2, M: 2, ShardSize: 100, FaultDomain: LevelHost,
		Shards: []ShardPlacement{
			{0, osdName(3, 1, 1)},
			{1, osdName(1, 1, 1)},
			{2, osdName(2, 1, 1)},
		},
	}
	plan := mustPlan(t, topo, []Stripe{stripe}, Options{})
	r := report(plan, "src-pick")
	if !r.Scheduled || len(r.Tasks) != 1 {
		t.Fatalf("expected scheduling, got %+v", r.Failures)
	}
	task := r.Tasks[0]
	if len(task.SourceOSDs) != 2 {
		t.Fatalf("expected exactly k=2 sources, got %v", task.SourceOSDs)
	}
	targetRack := topo.RackOf(task.TargetOSD)
	hasSameRack := false
	for _, src := range task.SourceOSDs {
		if topo.RackOf(src) == targetRack {
			hasSameRack = true
		}
	}
	if !hasSameRack {
		t.Fatalf("expected a same-rack source for target %s, got %v",
			task.TargetOSD, task.SourceOSDs)
	}
	if len(task.CrossRackSources) != 1 {
		t.Fatalf("expected exactly one cross-rack source, got %v", task.CrossRackSources)
	}

	remote := Stripe{
		ID: "src-remote", K: 2, M: 1, ShardSize: 100, FaultDomain: LevelRack,
		Shards: []ShardPlacement{
			{0, osdName(1, 1, 2)},
			{1, osdName(2, 1, 2)},
		},
	}
	plan2 := mustPlan(t, topo, []Stripe{remote}, Options{})
	task2 := report(plan2, "src-remote").Tasks[0]
	if topo.RackOf(task2.TargetOSD) != rackName(3) {
		t.Fatalf("expected target in rack3, got %s", task2.TargetOSD)
	}
	if len(task2.CrossRackSources) != 2 {
		t.Fatalf("expected both sources cross-rack, got %v", task2.CrossRackSources)
	}
}

// TestToRBandwidthQueuing verifies a tight rack egress limit forces tasks
// into multiple rounds while every round respects all node limits.
func TestToRBandwidthQueuing(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{
		perRackEgress: map[string]int64{rackName(1): 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	s1 := NewReplicatedStripe("tor-1", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 1, 1)}, {1, osdName(2, 1, 1)},
	})
	s2 := NewReplicatedStripe("tor-2", 3, 100, LevelRack, []ShardPlacement{
		{0, osdName(1, 2, 1)}, {1, osdName(2, 2, 1)},
	})
	plan := mustPlan(t, topo, []Stripe{s1, s2}, Options{
		GlobalConcurrent: 10,
		RoundDuration:    2 * time.Second,
	})

	if plan.TotalRounds < 2 {
		t.Fatalf("expected ToR bottleneck to span multiple rounds, got %d", plan.TotalRounds)
	}
	for _, round := range plan.Rounds {
		for id, usage := range round.NodeUsage {
			node, ok := topo.Node(id)
			if !ok {
				continue
			}
			if node.EgressLimit != Unlimited && usage.EgressBytes > node.EgressLimit {
				t.Fatalf("round %d node %s egress %d exceeds limit %d",
					round.Round, id, usage.EgressBytes, node.EgressLimit)
			}
			if node.IngressLimit != Unlimited && usage.IngressBytes > node.IngressLimit {
				t.Fatalf("round %d node %s ingress %d exceeds limit %d",
					round.Round, id, usage.IngressBytes, node.IngressLimit)
			}
			if node.MaxConcurrent > 0 && usage.ConcurrentTasks > node.MaxConcurrent {
				t.Fatalf("round %d node %s concurrency %d exceeds %d",
					round.Round, id, usage.ConcurrentTasks, node.MaxConcurrent)
			}
		}
		if usage := round.NodeUsage[rackName(1)]; usage.EgressBytes > 100 {
			t.Fatalf("rack1 egress %d exceeded 100 in round %d", usage.EgressBytes, round.Round)
		}
	}

	r1 := report(plan, "tor-1")
	r2 := report(plan, "tor-2")
	if r1.CompletedAt != 2*time.Second || r2.CompletedAt != 4*time.Second {
		t.Fatalf("unexpected completion times: %s %s", r1.CompletedAt, r2.CompletedAt)
	}
}
