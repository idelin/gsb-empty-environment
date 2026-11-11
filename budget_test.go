package netsec

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func buildRepeatedEvents(n int, stage string) []Event {
	events := make([]Event, n)
	for i := range events {
		events[i] = Event{ID: fmt.Sprintf("r%02d", i), Stage: stage, Time: int64(i)}
	}
	return events
}

// A deliberately tiny budget must still return a verified feasible
// assignment while honestly reporting that optimality was not proven.
func TestBudgetExhaustion(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "k1", Stage: "a", Time: 0},
			{ID: "k2", Stage: "b", Time: 0},
			{ID: "k3", Stage: "a", Time: 1},
			{ID: "k4", Stage: "b", Time: 1},
			{ID: "k5", Stage: "a", Time: 2},
			{ID: "k6", Stage: "b", Time: 2},
		},
		Templates: []ChainTemplate{
			{Name: "ab", Stages: []string{"a", "b"}},
		},
		MaxGap:     100,
		WorkBudget: 3,
	}
	got, err := Assign(req)
	if err != nil {
		t.Fatal(err)
	}
	assertFeasible(t, req, got)
	if got.ProvenOptimal || got.Complete {
		t.Fatalf("budget-limited result must not claim optimality: %+v", got)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty exhaustion reason")
	}

	// The same input with a proper budget must prove optimality.
	full := req
	full.WorkBudget = 0
	proven, err := Assign(full)
	if err != nil {
		t.Fatal(err)
	}
	if !proven.ProvenOptimal || len(proven.Chains) != 3 {
		t.Fatalf("expected 3 proven-optimal chains, got %+v", proven)
	}
}

func TestTimeBudgetExhaustion(t *testing.T) {
	req := Request{
		Events:      buildRepeatedEvents(24, "a"),
		Templates:   []ChainTemplate{{Name: "aa", Stages: []string{"a", "a"}}},
		MaxGap:      1 << 40,
		WorkBudget:  5,
		MaxDuration: time.Nanosecond,
	}
	got, err := Assign(req)
	if err != nil {
		t.Fatal(err)
	}
	assertFeasible(t, req, got)
	if got.ProvenOptimal {
		t.Fatalf("budget-boxed search must not claim optimality: %+v", got)
	}
}

func TestDeterminism(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "d1", Stage: "a", Time: 0},
			{ID: "d2", Stage: "b", Time: 1},
			{ID: "d3", Stage: "a", Time: 2},
			{ID: "d4", Stage: "b", Time: 3},
		},
		Templates: []ChainTemplate{
			{Name: "ab", Stages: []string{"a", "b"}},
		},
		MaxGap: 100,
	}
	first, err := Assign(req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Assign(req)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(again) != fmt.Sprint(first) {
			t.Fatalf("non-deterministic output:\n%+v\n%+v", first, again)
		}
	}
}

// Full 24-event input: 12 disjoint two-stage chains, exercising the MaxEvents
// boundary and mask bit 23.
func TestTwentyFourEvents(t *testing.T) {
	events := make([]Event, 0, 24)
	for i := 0; i < 12; i++ {
		events = append(events,
			Event{ID: fmt.Sprintf("lo%02d", i), Stage: "lo", Time: int64(2 * i)},
			Event{ID: fmt.Sprintf("hi%02d", i), Stage: "hi", Time: int64(2*i + 1)},
		)
	}
	req := Request{
		Events: events,
		Templates: []ChainTemplate{
			{Name: "chain", Stages: []string{"lo", "hi"}},
		},
		MaxGap: 100,
	}
	got, err := Assign(req)
	if err != nil {
		t.Fatal(err)
	}
	assertFeasible(t, req, got)
	// Every chain consumes at least two events and all 24 are usable, so 12
	// disjoint chains is the structural upper bound as well as the result.
	if !got.ProvenOptimal || len(got.Chains) != 12 || got.EventCount != 24 {
		t.Fatalf("expected 12 chains consuming all 24 events, got %+v", got)
	}
}

func TestEmptyInputs(t *testing.T) {
	got, err := Assign(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 0 || !got.ProvenOptimal {
		t.Fatalf("empty input should be an optimal empty assignment, got %+v", got)
	}

	noMatch := Request{
		Events:    []Event{{ID: "z", Stage: "x", Time: 0}},
		Templates: []ChainTemplate{{Name: "t", Stages: []string{"y"}}},
		MaxGap:    10,
	}
	got, err = Assign(noMatch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 0 || !got.ProvenOptimal {
		t.Fatalf("unmatched input should be an optimal empty assignment, got %+v", got)
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{
			name: "too many events",
			req: Request{
				Events:    buildRepeatedEvents(25, "a"),
				Templates: []ChainTemplate{{Name: "t", Stages: []string{"a"}}},
			},
		},
		{
			name: "duplicate event id",
			req: Request{Events: []Event{
				{ID: "q", Stage: "a", Time: 0},
				{ID: "q", Stage: "b", Time: 1},
			}},
		},
		{
			name: "empty event id",
			req:  Request{Events: []Event{{ID: "", Stage: "a", Time: 0}}},
		},
		{
			name: "unsorted events",
			req: Request{Events: []Event{
				{ID: "u1", Stage: "a", Time: 2},
				{ID: "u2", Stage: "a", Time: 1},
			}},
		},
		{name: "negative gap", req: Request{MaxGap: -1}},
		{name: "negative span", req: Request{MaxSpan: -1}},
		{
			name: "duplicate template",
			req: Request{Templates: []ChainTemplate{
				{Name: "t", Stages: []string{"a"}},
				{Name: "t", Stages: []string{"b"}},
			}},
		},
		{
			name: "empty stage",
			req: Request{Templates: []ChainTemplate{
				{Name: "t", Stages: []string{""}},
			}},
		},
		{
			name: "template without stages",
			req:  Request{Templates: []ChainTemplate{{Name: "t"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Assign(tc.req)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

// Randomized full-universe cross validation over many small instances with
// shared prefixes, repeated stages and contested middle events.
func TestRandomAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	stages := []string{"login", "pivot", "exploit", "exfil", "drop"}
	iterations := 300
	if testing.Short() {
		iterations = 50
	}
	for iter := 0; iter < iterations; iter++ {
		n := 1 + rng.Intn(10)
		events := make([]Event, n)
		clock := int64(0)
		for i := range events {
			clock += int64(rng.Intn(4))
			events[i] = Event{
				ID:    fmt.Sprintf("e%02d", i),
				Stage: stages[rng.Intn(len(stages))],
				Time:  clock,
			}
		}
		tplCount := 1 + rng.Intn(3)
		templates := make([]ChainTemplate, tplCount)
		for k := range templates {
			length := 1 + rng.Intn(3)
			tplStages := make([]string, length)
			for j := range tplStages {
				tplStages[j] = stages[rng.Intn(len(stages))]
			}
			templates[k] = ChainTemplate{
				Name:   fmt.Sprintf("t%d", k),
				Stages: tplStages,
			}
		}
		req := Request{
			Events:    events,
			Templates: templates,
			MaxGap:    int64(rng.Intn(8)),
		}
		if rng.Intn(2) == 0 {
			req.MaxSpan = int64(rng.Intn(15))
		}

		cands := oracleEnumerateCandidates(req)
		if len(cands) > oracleMaxCandidates {
			// Keep 2^|candidates| oracle runs cheap; the production solver
			// still has to handle the instance without error below.
			got, err := Assign(req)
			if err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
			assertFeasible(t, req, got)
			continue
		}
		assertMatchesOracle(t, req)
	}
}
