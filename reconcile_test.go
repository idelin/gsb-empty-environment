package main

import (
	"reflect"
	"testing"
)

func set(field string, v any) Op   { return Op{Kind: "set", Field: field, Value: v} }
func add(field string, d int64) Op { return Op{Kind: "add", Field: field, Delta: d} }
func del(field string) Op          { return Op{Kind: "del", Field: field} }

// Causality wins over timestamps: e2 has an EARLIER timestamp but declares
// e1 as its cause, so e1 must be applied first.
func TestCausalOrderBeatsTimestamp(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "e2", Time: 10, Causes: []string{"e1"}, Op: set("status", "paid")},
		{ID: "e1", Time: 99, Op: set("status", "open")},
	})
	if res.Status != "unique" {
		t.Fatalf("status = %q, want unique", res.Status)
	}
	if res.State["status"] != "paid" {
		t.Fatalf("state = %v, want status=paid", res.State)
	}
}

// Concurrent writes to the same field yield multiple legal final states.
func TestAmbiguousConcurrentWrites(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "a", Op: set("owner", "alice")},
		{ID: "b", Op: set("owner", "bob")},
	})
	if res.Status != "ambiguous" {
		t.Fatalf("status = %q, want ambiguous", res.Status)
	}
	if len(res.States) != 2 {
		t.Fatalf("got %d states, want 2: %v", len(res.States), res.States)
	}
}

// Commuting operations converge to a single state despite free ordering.
func TestCommutingAddsAreUnique(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "a", Op: add("balance", 5)},
		{ID: "b", Op: add("balance", 7)},
		{ID: "c", Causes: []string{"a"}, Op: add("balance", -2)},
	})
	if res.Status != "unique" || res.State["balance"] != float64(10) {
		t.Fatalf("got %+v, want unique balance=10", res)
	}
}

// The same event observed via different sources (with different transport
// metadata and skewed timestamps) is deduplicated.
func TestDuplicatesDeduplicated(t *testing.T) {
	e := Event{ID: "a", Time: 1, Source: "s1", Op: add("n", 1)}
	dup := e
	dup.Source = "s2" // same event, different source and timestamp
	dup.Time = 999
	res := Reconcile([]Event{e, dup, dup})
	if res.Status != "unique" || res.State["n"] != float64(1) {
		t.Fatalf("got %+v, want unique n=1", res)
	}
}

// Same id, different content: contradiction with minimal subset = that id.
func TestConflictingDuplicates(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "a", Op: set("x", "1")},
		{ID: "b", Op: set("y", "2")},
		{ID: "a", Op: set("x", "9")},
	})
	if res.Status != "contradiction" {
		t.Fatalf("status = %q, want contradiction", res.Status)
	}
	if !reflect.DeepEqual(res.Subset, []string{"a"}) {
		t.Fatalf("subset = %v, want [a]", res.Subset)
	}
}

// Missing causal references are tolerated and reported, not fatal.
func TestMissingCausesTolerated(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "b", Causes: []string{"ghost"}, Op: set("status", "paid")},
	})
	if res.Status != "unique" || res.State["status"] != "paid" {
		t.Fatalf("got %+v, want unique status=paid", res)
	}
	if !reflect.DeepEqual(res.Missing, []string{"ghost"}) {
		t.Fatalf("missing = %v, want [ghost]", res.Missing)
	}
}

// A causal cycle is a contradiction; the minimal subset is the cycle itself.
func TestCausalCycle(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "a", Causes: []string{"c"}, Op: set("x", "a")},
		{ID: "b", Causes: []string{"a"}, Op: set("x", "b")},
		{ID: "c", Causes: []string{"b"}, Op: set("x", "c")},
		{ID: "d", Op: set("y", "d")}, // unrelated, must not appear in subset
	})
	if res.Status != "contradiction" || res.Reason != "causal cycle" {
		t.Fatalf("got %+v, want causal-cycle contradiction", res)
	}
	if !reflect.DeepEqual(res.Subset, []string{"a", "b", "c"}) {
		t.Fatalf("subset = %v, want [a b c]", res.Subset)
	}
}

// Self-dependency is a minimal contradiction of size 1.
func TestSelfLoop(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "a", Causes: []string{"a"}, Op: set("x", "a")},
	})
	if res.Status != "contradiction" || !reflect.DeepEqual(res.Subset, []string{"a"}) {
		t.Fatalf("got %+v, want contradiction subset [a]", res)
	}
}

// del and set interact: order matters, so both outcomes must appear.
func TestSetDeleteAmbiguity(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "a", Op: set("k", "v")},
		{ID: "b", Op: del("k")},
	})
	if res.Status != "ambiguous" || len(res.States) != 2 {
		t.Fatalf("got %+v, want 2 ambiguous states", res)
	}
}

// A diamond of causality with commuting leaves stays unique.
func TestDiamondUnique(t *testing.T) {
	res := Reconcile([]Event{
		{ID: "root", Op: set("f1", "r")},
		{ID: "l", Causes: []string{"root"}, Op: add("n", 1)},
		{ID: "r", Causes: []string{"root"}, Op: add("n", 2)},
		{ID: "join", Causes: []string{"l", "r"}, Op: set("done", true)},
	})
	want := State{"f1": "r", "n": float64(3), "done": true}
	if res.Status != "unique" || !reflect.DeepEqual(res.State, want) {
		t.Fatalf("got %+v, want unique %v", res, want)
	}
}

// Twenty unconstrained events: 20! orders but few distinct states; the DP
// must stay fast and correct.
func TestTwentyEvents(t *testing.T) {
	evs := make([]Event, 20)
	for i := range evs {
		evs[i] = Event{ID: string(rune('a' + i)), Op: add("n", 1)}
	}
	res := Reconcile(evs)
	if res.Status != "unique" || res.State["n"] != float64(20) {
		t.Fatalf("got %+v, want unique n=20", res)
	}
}

// Empty log reconstructs the empty object, uniquely.
func TestEmpty(t *testing.T) {
	res := Reconcile(nil)
	if res.Status != "unique" || len(res.State) != 0 {
		t.Fatalf("got %+v, want unique empty state", res)
	}
}
