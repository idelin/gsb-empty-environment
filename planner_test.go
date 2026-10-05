package main

import (
	"reflect"
	"testing"
	"time"
)

func ts(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func batchTimes(p Plan) map[string]time.Time {
	m := make(map[string]time.Time)
	for _, b := range p.Batches {
		for _, o := range b.Objects {
			m[o] = b.Time
		}
	}
	return m
}

func blockedChains(p Plan) map[string][]Step {
	m := make(map[string][]Step)
	for _, b := range p.PermanentlyBlocked {
		m[b.Object] = b.Chain
	}
	return m
}

func TestRetentionOnly(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2026-01-01T00:00:00Z")},
		{ID: "b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := batchTimes(plan)
	if !got["a"].Equal(*ts("2026-01-01T00:00:00Z")) {
		t.Errorf("a: got %v", got["a"])
	}
	if !got["b"].IsZero() {
		t.Errorf("b should be immediately deletable, got %v", got["b"])
	}
	if len(plan.PermanentlyBlocked) != 0 {
		t.Errorf("unexpected blocked: %+v", plan.PermanentlyBlocked)
	}
}

func TestStrongRefBlocksWeakRefDoesNot(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2026-01-01T00:00:00Z")},
		{ID: "b", RetentionUntil: ts("2027-06-01T00:00:00Z"), StrongRefs: []string{"a"}},
		{ID: "c", RetentionUntil: ts("2026-03-01T00:00:00Z"), WeakRefs: []string{"a"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := batchTimes(plan)
	// a is strongly referenced by b, so it inherits b's later time.
	if !got["a"].Equal(*ts("2027-06-01T00:00:00Z")) {
		t.Errorf("a: got %v", got["a"])
	}
	// c's weak reference to a must not delay a... and c keeps its own time.
	if !got["c"].Equal(*ts("2026-03-01T00:00:00Z")) {
		t.Errorf("c: got %v", got["c"])
	}
}

func TestCycleTreatedAsUnit(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2026-01-01T00:00:00Z"), StrongRefs: []string{"b"}},
		{ID: "b", RetentionUntil: ts("2028-01-01T00:00:00Z"), StrongRefs: []string{"a"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Batches) != 1 {
		t.Fatalf("expected 1 batch, got %+v", plan.Batches)
	}
	b := plan.Batches[0]
	if !b.Time.Equal(*ts("2028-01-01T00:00:00Z")) {
		t.Errorf("batch time: got %v", b.Time)
	}
	if !reflect.DeepEqual(b.Objects, []string{"a", "b"}) {
		t.Errorf("batch objects: got %v", b.Objects)
	}
}

func TestPermanentHoldPropagatesAlongStrongRefs(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "c", LegalHold: true, StrongRefs: []string{"d"}},
		{ID: "d", StrongRefs: []string{"e"}},
		{ID: "e"},
		{ID: "f", RetentionUntil: ts("2026-01-01T00:00:00Z")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	chains := blockedChains(plan)
	for _, id := range []string{"c", "d", "e"} {
		if _, ok := chains[id]; !ok {
			t.Errorf("%s should be permanently blocked", id)
		}
	}
	want := []Step{
		{Object: "e", Reason: "strongly referenced by d"},
		{Object: "d", Reason: "strongly referenced by c"},
		{Object: "c", Reason: "legal hold with no expiry (permanent)"},
	}
	if !reflect.DeepEqual(chains["e"], want) {
		t.Errorf("chain for e:\n got %+v\nwant %+v", chains["e"], want)
	}
	if len(plan.Batches) != 1 || !reflect.DeepEqual(plan.Batches[0].Objects, []string{"f"}) {
		t.Errorf("batches: got %+v", plan.Batches)
	}
}

func TestHoldInsideCycleBlocksWholeCycle(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "a", StrongRefs: []string{"b"}},
		{ID: "b", LegalHold: true, StrongRefs: []string{"a"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	chains := blockedChains(plan)
	want := []Step{
		{Object: "a", Reason: "in strong-reference cycle with b; cycle is deleted as a unit"},
		{Object: "b", Reason: "legal hold with no expiry (permanent)"},
	}
	if !reflect.DeepEqual(chains["a"], want) {
		t.Errorf("chain for a:\n got %+v\nwant %+v", chains["a"], want)
	}
	if len(chains["b"]) != 1 {
		t.Errorf("chain for b: got %+v", chains["b"])
	}
}

func TestHoldUntilIsTemporary(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "a", LegalHold: true, HoldUntil: ts("2030-05-01T00:00:00Z"), RetentionUntil: ts("2026-01-01T00:00:00Z")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PermanentlyBlocked) != 0 {
		t.Fatalf("unexpected blocked: %+v", plan.PermanentlyBlocked)
	}
	got := batchTimes(plan)
	if !got["a"].Equal(*ts("2030-05-01T00:00:00Z")) {
		t.Errorf("a: got %v", got["a"])
	}
}

func TestBatchesGroupedAndSorted(t *testing.T) {
	plan, err := PlanDeletion(Input{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2027-01-01T00:00:00Z")},
		{ID: "b", RetentionUntil: ts("2026-01-01T00:00:00Z")},
		{ID: "c", RetentionUntil: ts("2027-01-01T00:00:00Z")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Batches) != 2 {
		t.Fatalf("expected 2 batches, got %+v", plan.Batches)
	}
	if !reflect.DeepEqual(plan.Batches[0].Objects, []string{"b"}) {
		t.Errorf("first batch: got %+v", plan.Batches[0])
	}
	if !reflect.DeepEqual(plan.Batches[1].Objects, []string{"a", "c"}) {
		t.Errorf("second batch: got %+v", plan.Batches[1])
	}
}

func TestValidationErrors(t *testing.T) {
	if _, err := PlanDeletion(Input{Objects: []ObjectInput{{ID: "a"}, {ID: "a"}}}); err == nil {
		t.Error("expected duplicate id error")
	}
	if _, err := PlanDeletion(Input{Objects: []ObjectInput{{ID: "a", StrongRefs: []string{"x"}}}}); err == nil {
		t.Error("expected unknown strong ref error")
	}
	if _, err := PlanDeletion(Input{Objects: []ObjectInput{{ID: "a", WeakRefs: []string{"x"}}}}); err == nil {
		t.Error("expected unknown weak ref error")
	}
}
