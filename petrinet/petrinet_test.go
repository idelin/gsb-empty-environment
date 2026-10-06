package petrinet

import (
	"reflect"
	"strings"
	"testing"
)

func TestShortestSequence(t *testing.T) {
	net := &Net{
		Places:     []string{"A", "B", "C"},
		Capacities: []int{4, 4, 4},
		Transitions: []Transition{
			{Name: "A->B", Consume: []int{1, 0, 0}, Produce: []int{0, 1, 0}},
			{Name: "B->C", Consume: []int{0, 1, 0}, Produce: []int{0, 0, 1}},
			{Name: "A->C", Consume: []int{1, 0, 0}, Produce: []int{0, 0, 1}},
		},
	}
	res, err := net.Check([]int{1, 0, 0}, []int{0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable")
	}
	if want := []int{2}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("shortest sequence = %v, want %v", res.Sequence, want)
	}
	if res.Explored != 1 {
		t.Fatalf("explored = %d, want 1", res.Explored)
	}
}

func TestCapacityDisablesTransition(t *testing.T) {
	net := &Net{
		Places:     []string{"P"},
		Capacities: []int{2},
		Transitions: []Transition{
			{Name: "produce2", Consume: []int{0}, Produce: []int{2}},
		},
	}
	if net.Enabled([]int{1}, 0) {
		t.Fatal("transition producing beyond capacity must be disabled")
	}
	next, ok := net.Fire([]int{0}, 0)
	if !ok {
		t.Fatal("transition within capacity must be enabled")
	}
	if !reflect.DeepEqual(next, []int{2}) {
		t.Fatalf("next = %v, want [2]", next)
	}
}

func TestConsumeBeforeProduceSelfLoop(t *testing.T) {
	net := &Net{
		Places:     []string{"P"},
		Capacities: []int{1},
		Transitions: []Transition{
			{Name: "selfloop", Consume: []int{1}, Produce: []int{1}},
		},
	}
	// With capacity 1 and one token in P, the self-loop is enabled only
	// because consumption happens before production; producing first
	// would transiently exceed the capacity.
	if !net.Enabled([]int{1}, 0) {
		t.Fatal("self-loop at full capacity must be enabled under consume-then-produce semantics")
	}
	next, ok := net.Fire([]int{1}, 0)
	if !ok || !reflect.DeepEqual(next, []int{1}) {
		t.Fatalf("Fire = %v, %v; want [1], true", next, ok)
	}
	// Without a token to consume, the self-loop is disabled.
	if net.Enabled([]int{0}, 0) {
		t.Fatal("self-loop without tokens must be disabled")
	}
}

func TestSelfLoopDoesNotHangBFS(t *testing.T) {
	net := &Net{
		Places:     []string{"P", "Q"},
		Capacities: []int{1, 1},
		Transitions: []Transition{
			{Name: "selfloop", Consume: []int{1, 0}, Produce: []int{1, 0}},
			{Name: "P->Q", Consume: []int{1, 0}, Produce: []int{0, 1}},
		},
	}
	res, err := net.Check([]int{1, 0}, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable")
	}
	if want := []int{1}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("sequence = %v, want %v", res.Sequence, want)
	}
	if res.Explored != 1 {
		t.Fatalf("explored = %d, want 1", res.Explored)
	}
}

func TestZeroConsumptionTransition(t *testing.T) {
	net := &Net{
		Places:     []string{"P"},
		Capacities: []int{3},
		Transitions: []Transition{
			{Name: "generate", Consume: []int{0}, Produce: []int{1}},
		},
	}
	res, err := net.Check([]int{0}, []int{3})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable {
		t.Fatal("target should be reachable")
	}
	if want := []int{0, 0, 0}; !reflect.DeepEqual(res.Sequence, want) {
		t.Fatalf("sequence = %v, want %v", res.Sequence, want)
	}
	if res.Explored != 3 {
		t.Fatalf("explored = %d, want 3", res.Explored)
	}
}

func TestUnreachableExploresCompleteStateSpace(t *testing.T) {
	net := &Net{
		Places:     []string{"P", "Q"},
		Capacities: []int{1, 1},
		Transitions: []Transition{
			{Name: "P->Q", Consume: []int{1, 0}, Produce: []int{0, 1}},
		},
	}
	res, err := net.Check([]int{1, 0}, []int{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reachable {
		t.Fatal("target {1,1} must be unreachable")
	}
	// Complete reachable state space: {1,0} and {0,1}.
	if res.Explored != 2 {
		t.Fatalf("explored = %d, want 2 (complete state space)", res.Explored)
	}
}

func TestZeroConsumptionUnboundedByCapacityProof(t *testing.T) {
	// Zero-consumption production is bounded only by capacity; a target
	// beyond capacity is rejected, a target within capacity is found.
	net := &Net{
		Places:     []string{"P"},
		Capacities: []int{2},
		Transitions: []Transition{
			{Name: "generate", Consume: []int{0}, Produce: []int{1}},
		},
	}
	if _, err := net.Check([]int{0}, []int{3}); err == nil {
		t.Fatal("target beyond capacity must be rejected")
	}
	res, err := net.Check([]int{0}, []int{2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reachable || len(res.Sequence) != 2 {
		t.Fatalf("got reachable=%v seq=%v, want reachable with 2 steps", res.Reachable, res.Sequence)
	}
}

func TestVerifyRejectsBadSequences(t *testing.T) {
	net := &Net{
		Places:     []string{"P", "Q"},
		Capacities: []int{1, 1},
		Transitions: []Transition{
			{Name: "P->Q", Consume: []int{1, 0}, Produce: []int{0, 1}},
		},
	}
	initial := []int{1, 0}
	target := []int{0, 1}

	if err := net.Verify(initial, target, []int{0}); err != nil {
		t.Fatalf("valid sequence rejected: %v", err)
	}
	if err := net.Verify(initial, target, []int{0, 0}); err == nil ||
		!strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled step not detected: %v", err)
	}
	if err := net.Verify(initial, target, []int{}); err == nil {
		t.Fatal("wrong final marking not detected")
	}
	if err := net.Verify(initial, target, []int{7}); err == nil {
		t.Fatal("invalid transition index not detected")
	}
}

func TestInvalidNetRejected(t *testing.T) {
	net := &Net{
		Places:     []string{"P"},
		Capacities: []int{1},
		Transitions: []Transition{
			{Name: "bad", Consume: []int{1, 0}, Produce: []int{0}},
		},
	}
	if _, err := net.Check([]int{0}, []int{1}); err == nil {
		t.Fatal("structurally invalid net must be rejected")
	}
}
