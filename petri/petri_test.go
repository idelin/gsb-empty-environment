package petri

import (
	"reflect"
	"testing"
)

// twoPlaceNet builds a net with two places and the given transitions.
func twoPlaceNet(cap0, cap1 int, trs ...Transition) Net {
	return Net{Capacity: []int{cap0, cap1}, Transitions: trs}
}

func TestShortestSequence(t *testing.T) {
	// t0: move one token p0 -> p1; t1: move one token p1 -> p0.
	net := twoPlaceNet(3, 3,
		Transition{Name: "t0", Consume: []int{1, 0}, Produce: []int{0, 1}},
		Transition{Name: "t1", Consume: []int{0, 1}, Produce: []int{1, 0}},
	)
	res, err := Solve(net, Marking{2, 0}, Marking{0, 2})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable")
	}
	if want := []int{0, 0}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("sequence = %v, want shortest %v", res.Sequence, want)
	}
	if res.Explored <= 0 {
		t.Fatalf("explored = %d, want > 0", res.Explored)
	}
}

func TestUnreachableExploresCompleteStateSpace(t *testing.T) {
	// Token count is invariant at 2, so {0,0} is unreachable. Reachable
	// markings: {2,0}, {1,1}, {0,2} — exactly 3 states must be explored.
	net := twoPlaceNet(2, 2,
		Transition{Name: "t0", Consume: []int{1, 0}, Produce: []int{0, 1}},
		Transition{Name: "t1", Consume: []int{0, 1}, Produce: []int{1, 0}},
	)
	res, err := Solve(net, Marking{2, 0}, Marking{0, 0})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Reachable {
		t.Fatal("target should be unreachable")
	}
	if res.Sequence != nil {
		t.Fatalf("sequence = %v, want nil", res.Sequence)
	}
	if res.Explored != 3 {
		t.Fatalf("explored = %d, want 3 (complete reachable state space)", res.Explored)
	}
}

func TestCapacityDisablesTransition(t *testing.T) {
	// t0 would produce into p1 which is already at capacity 1: disabled.
	net := twoPlaceNet(1, 1,
		Transition{Name: "t0", Consume: []int{1, 0}, Produce: []int{0, 1}},
	)
	res, err := Solve(net, Marking{1, 1}, Marking{0, 1})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Reachable {
		t.Fatal("target should be unreachable: transition disabled by capacity")
	}
	if res.Explored != 1 {
		t.Fatalf("explored = %d, want 1", res.Explored)
	}
}

func TestConsumeBeforeProduceAtCapacity(t *testing.T) {
	// Self-loop: consume 1 from p0 then produce 1 back into p0. With p0 at
	// capacity the net effect stays within bounds, so the transition must
	// be enabled — this only holds with consume-then-produce semantics.
	net := Net{
		Capacity: []int{1},
		Transitions: []Transition{
			{Name: "loop", Consume: []int{1}, Produce: []int{1}},
		},
	}
	if _, ok := net.Fire(Marking{1}, 0); !ok {
		t.Fatal("self-loop at capacity must be enabled (consume before produce)")
	}
	// A pure producer into a full place is disabled.
	net2 := Net{
		Capacity: []int{1},
		Transitions: []Transition{
			{Name: "gen", Consume: []int{0}, Produce: []int{1}},
		},
	}
	if _, ok := net2.Fire(Marking{1}, 0); ok {
		t.Fatal("pure producer into a full place must be disabled")
	}
}

func TestSelfLoopArcInSearch(t *testing.T) {
	// t0 is a self-loop on p0 (no net effect, but needs a token present);
	// t1 drains p0 into p1. Target requires firing t1 after the loop.
	net := twoPlaceNet(2, 2,
		Transition{Name: "loop", Consume: []int{1, 0}, Produce: []int{1, 0}},
		Transition{Name: "move", Consume: []int{1, 0}, Produce: []int{0, 1}},
	)
	res, err := Solve(net, Marking{1, 0}, Marking{0, 1})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable")
	}
	if want := []int{1}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("sequence = %v, want %v (self-loop must not appear in shortest path)", res.Sequence, want)
	}
	// The self-loop never changes the marking, so the state space is tiny.
	if res.Explored != 2 {
		t.Fatalf("explored = %d, want 2", res.Explored)
	}
}

func TestZeroConsumptionTransition(t *testing.T) {
	// t0 consumes nothing and produces one token into p0 (capacity 2).
	net := Net{
		Capacity: []int{2},
		Transitions: []Transition{
			{Name: "gen", Consume: []int{0}, Produce: []int{1}},
		},
	}
	res, err := Solve(net, Marking{0}, Marking{2})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable via zero-consumption transition")
	}
	if want := []int{0, 0}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("sequence = %v, want %v", res.Sequence, want)
	}
	// Capacity bounds the generator: states {0}, {1}, {2} only.
	res2, err := Solve(net, Marking{0}, Marking{0})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res2.Reachable || len(res2.Sequence) != 0 {
		t.Fatalf("initial==target: got %+v, want reachable with empty sequence", res2)
	}
}

func TestTargetEqualsInitial(t *testing.T) {
	net := twoPlaceNet(1, 1,
		Transition{Name: "t0", Consume: []int{1, 0}, Produce: []int{0, 1}},
	)
	res, err := Solve(net, Marking{1, 0}, Marking{1, 0})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Reachable || len(res.Sequence) != 0 {
		t.Fatalf("got %+v, want reachable with empty sequence", res)
	}
	if res.Explored != 1 {
		t.Fatalf("explored = %d, want 1", res.Explored)
	}
}

func TestTargetOutOfCapacity(t *testing.T) {
	net := twoPlaceNet(1, 1)
	res, err := Solve(net, Marking{0, 0}, Marking{0, 5})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Reachable || res.Explored != 0 {
		t.Fatalf("got %+v, want unreachable with 0 explored", res)
	}
}

func TestReplayIndependentVerification(t *testing.T) {
	net := twoPlaceNet(3, 3,
		Transition{Name: "t0", Consume: []int{1, 0}, Produce: []int{0, 1}},
		Transition{Name: "t1", Consume: []int{0, 1}, Produce: []int{1, 0}},
	)
	initial := Marking{2, 0}
	target := Marking{1, 1}
	res, err := Solve(net, initial, target)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// Independently re-execute the sequence step by step.
	final, err := net.Replay(initial, res.Sequence)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !reflect.DeepEqual(final, target) {
		t.Fatalf("replay ended at %v, want %v", final, target)
	}
	// Replaying a disabled step must fail.
	if _, err := net.Replay(Marking{0, 0}, []int{0}); err == nil {
		t.Fatal("Replay of a disabled transition must fail")
	}
	// Invalid transition index must fail.
	if _, err := net.Replay(initial, []int{7}); err == nil {
		t.Fatal("Replay with invalid transition index must fail")
	}
}

func TestShortestAmongCompetingPaths(t *testing.T) {
	// t0 moves p0->p1 directly (1 step); t1+t2 do it via p2 (2 steps).
	net := Net{
		Capacity: []int{1, 1, 1},
		Transitions: []Transition{
			{Name: "direct", Consume: []int{1, 0, 0}, Produce: []int{0, 1, 0}},
			{Name: "hop1", Consume: []int{1, 0, 0}, Produce: []int{0, 0, 1}},
			{Name: "hop2", Consume: []int{0, 0, 1}, Produce: []int{0, 1, 0}},
		},
	}
	res, err := Solve(net, Marking{1, 0, 0}, Marking{0, 1, 0})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable")
	}
	if want := []int{0}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("sequence = %v, want shortest %v", res.Sequence, want)
	}
}

func TestValidationErrors(t *testing.T) {
	bad := Net{Capacity: []int{1}, Transitions: []Transition{
		{Name: "bad", Consume: []int{1, 0}, Produce: []int{0}},
	}}
	if _, err := Solve(bad, Marking{0}, Marking{0}); err == nil {
		t.Fatal("Solve must reject malformed transitions")
	}
	net := Net{Capacity: []int{1}}
	if _, err := Solve(net, Marking{3}, Marking{0}); err == nil {
		t.Fatal("Solve must reject an initial marking above capacity")
	}
}
