package main

import (
	"math/rand"
	"reflect"
	"testing"
)

func add(tick, u, v int) event   { return event{tick: tick, op: opAdd, u: u, v: v} }
func del(tick, u, v int) event   { return event{tick: tick, op: opDel, u: u, v: v} }
func query(tick, u, v int) event { return event{tick: tick, op: opQuery, u: u, v: v} }

// runSolve solves and cross-checks against per-tick rebuilt graphs.
func runSolve(t *testing.T, events []event) []answer {
	t.Helper()
	answers, err := solve(events)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := verifyByRebuild(events, answers); err != nil {
		t.Fatalf("verifyByRebuild: %v", err)
	}
	return answers
}

func TestSameTickOrdering(t *testing.T) {
	events := []event{
		query(0, 1, 2), // before the add at the same tick: disconnected
		add(0, 1, 2),
		query(0, 1, 2), // after the add: connected
		del(0, 1, 2),
		query(0, 1, 2), // after the delete: disconnected again
	}
	answers := runSolve(t, events)
	want := []bool{false, true, false}
	for i, a := range answers {
		if a.connected != want[i] {
			t.Fatalf("answer %d: connected=%v, want %v", i, a.connected, want[i])
		}
	}
	if got := answers[1].path; !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("path = %v, want [1 2]", got)
	}
}

func TestSameTickAddAndDeleteInvisible(t *testing.T) {
	// An edge opened and closed within one tick, before the query, must
	// never be visible (its alive interval is empty).
	events := []event{
		add(0, 1, 2),
		del(0, 1, 2),
		query(0, 1, 2),
	}
	answers := runSolve(t, events)
	if answers[0].connected {
		t.Fatalf("edge opened and closed before the query must not connect 1 and 2")
	}
}

func TestEdgeReopenedManyTimes(t *testing.T) {
	events := []event{
		add(0, 1, 2),
		query(1, 1, 2), // connected
		del(2, 1, 2),
		query(3, 1, 2), // disconnected
		add(4, 1, 2),
		query(5, 1, 2), // connected
		del(6, 1, 2),
		add(7, 1, 2),
		query(8, 1, 2), // connected
		del(9, 1, 2),
		query(10, 1, 2), // disconnected
	}
	answers := runSolve(t, events)
	want := []bool{true, false, true, true, false}
	for i, a := range answers {
		if a.connected != want[i] {
			t.Fatalf("answer %d (tick %d): connected=%v, want %v", i, a.tick, a.connected, want[i])
		}
	}
}

func TestReferenceCounting(t *testing.T) {
	events := []event{
		add(0, 1, 2),
		add(1, 1, 2), // duplicate: count = 2
		del(2, 1, 2), // count = 1, edge still alive
		query(3, 1, 2),
		del(4, 1, 2), // count = 0, edge gone
		query(5, 1, 2),
	}
	answers := runSolve(t, events)
	if !answers[0].connected {
		t.Fatalf("one delete of a twice-added edge must not disconnect")
	}
	if answers[1].connected {
		t.Fatalf("second delete must remove the edge")
	}
}

func TestDeleteNonExistent(t *testing.T) {
	for _, events := range [][]event{
		{del(0, 1, 2)}, // never added
		{add(0, 1, 2), del(1, 1, 2), del(2, 1, 2)}, // deleted twice
		{add(0, 1, 2), del(1, 2, 1), del(2, 1, 2)}, // undirected: 2-1 == 1-2
	} {
		if _, err := solve(events); err == nil {
			t.Fatalf("events %v: expected delete-of-non-existent error, got nil", events)
		}
		if err := verifyByRebuild(events, nil); err == nil {
			t.Fatalf("verifyByRebuild: events %v: expected error, got nil", events)
		}
	}
}

func TestAnswersMatchQueryTimeNotFinalGraph(t *testing.T) {
	// The final graph has edge 1-2 present; early queries must still see
	// it absent, and a query after its removal must see it gone.
	events := []event{
		query(0, 1, 2),
		add(1, 1, 2),
		query(2, 1, 2),
		del(3, 1, 2),
		add(4, 1, 2), // final graph: edge present
	}
	answers := runSolve(t, events)
	if answers[0].connected {
		t.Fatalf("query at tick 0 answered from final graph: edge not yet added")
	}
	if !answers[1].connected {
		t.Fatalf("query at tick 2 should see the edge added at tick 1")
	}
}

func TestPathAndCutContent(t *testing.T) {
	events := []event{
		add(0, 1, 2),
		add(0, 2, 3),
		add(0, 4, 5),
		query(1, 1, 3), // path 1-2-3
		query(1, 1, 4), // cut {1 2 3} | {4 5}
	}
	answers := runSolve(t, events)
	if got := answers[0].path; !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("path = %v, want [1 2 3]", got)
	}
	a := answers[1]
	if !reflect.DeepEqual(a.comp, []int{1, 2, 3}) || !reflect.DeepEqual(a.rest, []int{4, 5}) {
		t.Fatalf("cut = %v | %v, want [1 2 3] | [4 5]", a.comp, a.rest)
	}
}

func TestSelfQuery(t *testing.T) {
	answers := runSolve(t, []event{query(0, 7, 7)})
	if !answers[0].connected || !reflect.DeepEqual(answers[0].path, []int{7}) {
		t.Fatalf("self query must be connected with trivial path, got %+v", answers[0])
	}
}

func TestUnaddedEdgeNeverVisible(t *testing.T) {
	// An edge added only in the future must not affect earlier queries.
	events := []event{
		query(0, 1, 2),
		add(5, 1, 2),
	}
	answers := runSolve(t, events)
	if answers[0].connected {
		t.Fatalf("future edge leaked into an earlier query")
	}
	if answers[0].rest == nil {
		t.Fatalf("disconnected answer must carry the partition evidence")
	}
}

func TestRandomFuzzAgainstRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 200; trial++ {
		const vertices = 9
		n := 1 + rng.Intn(300)
		events := make([]event, 0, n)
		count := make(map[edgeKey]int)
		tick := 0
		for i := 0; i < n; i++ {
			if rng.Intn(3) > 0 { // often stay on the same tick
				tick += rng.Intn(3)
			}
			u, v := rng.Intn(vertices), rng.Intn(vertices)
			op := rng.Intn(3)
			switch op {
			case 0:
				events = append(events, add(tick, u, v))
				count[makeEdge(u, v)]++
			case 1:
				k := makeEdge(u, v)
				if count[k] == 0 {
					// keep the stream valid: turn into an add
					events = append(events, add(tick, u, v))
					count[k]++
				} else {
					events = append(events, del(tick, u, v))
					count[k]--
				}
			default:
				events = append(events, query(tick, u, v))
			}
		}
		answers, err := solve(events)
		if err != nil {
			t.Fatalf("trial %d: solve: %v", trial, err)
		}
		if err := verifyByRebuild(events, answers); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

func TestRandomFuzzDetectsInvalidDelete(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		var events []event
		count := make(map[edgeKey]int)
		tick := 0
		// Build a valid stream, then append one guaranteed-invalid delete.
		for i := 0; i < 50; i++ {
			tick += rng.Intn(2)
			u, v := rng.Intn(6), rng.Intn(6)
			if rng.Intn(2) == 0 {
				events = append(events, add(tick, u, v))
				count[makeEdge(u, v)]++
			} else if k := makeEdge(u, v); count[k] > 0 {
				events = append(events, del(tick, u, v))
				count[k]--
			}
		}
		events = append(events, del(tick+1, 100, 200)) // never added
		if _, err := solve(events); err == nil {
			t.Fatalf("trial %d: expected error for invalid delete", trial)
		}
	}
}
