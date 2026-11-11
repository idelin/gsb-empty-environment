package netsec

import (
	"fmt"
	"math/rand"
	"testing"
)

func BenchmarkAssign24Disjoint(b *testing.B) {
	events := make([]Event, 0, 24)
	for i := 0; i < 12; i++ {
		events = append(events,
			Event{ID: fmt.Sprintf("lo%02d", i), Stage: "lo", Time: int64(2 * i)},
			Event{ID: fmt.Sprintf("hi%02d", i), Stage: "hi", Time: int64(2*i + 1)},
		)
	}
	req := Request{
		Events:    events,
		Templates: []ChainTemplate{{Name: "chain", Stages: []string{"lo", "hi"}}},
		MaxGap:    100,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Assign(req); err != nil {
			b.Fatal(err)
		}
	}
}

// Adversarial: every event shares one stage so two-stage candidates form a
// dense conflict graph, the largest realistic search space at 24 events.
func BenchmarkAssign24Dense(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	events := make([]Event, 24)
	clock := int64(0)
	for i := range events {
		clock += int64(rng.Intn(3))
		events[i] = Event{ID: fmt.Sprintf("d%02d", i), Stage: "a", Time: clock}
	}
	req := Request{
		Events:    events,
		Templates: []ChainTemplate{{Name: "aa", Stages: []string{"a", "a"}}},
		MaxGap:    1 << 30,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := Assign(req)
		if err != nil {
			b.Fatal(err)
		}
		if !got.ProvenOptimal {
			b.Fatalf("dense case did not prove optimality: %s", got.Reason)
		}
	}
}

func TestDenseWithinDefaultBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	events := make([]Event, 24)
	clock := int64(0)
	for i := range events {
		clock += int64(rng.Intn(3))
		events[i] = Event{ID: fmt.Sprintf("d%02d", i), Stage: "a", Time: clock}
	}
	req := Request{
		Events:    events,
		Templates: []ChainTemplate{{Name: "aa", Stages: []string{"a", "a"}}},
		MaxGap:    1 << 30,
	}
	got, err := Assign(req)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ProvenOptimal {
		t.Fatalf("default budget should cover the dense 24-event case: %s (%d chains)",
			got.Reason, len(got.Chains))
	}
	if len(got.Chains) != 12 {
		t.Fatalf("expected 12 disjoint pairs, got %d", len(got.Chains))
	}
}
