package main

import (
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

func resultOf(t *testing.T, out PlanOutput, id string) ObjectResult {
	t.Helper()
	for _, r := range out.Objects {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for object %q", id)
	return ObjectResult{}
}

func TestRetentionOnly(t *testing.T) {
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2027-01-01T00:00:00Z")},
		{ID: "b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a := resultOf(t, out, "a")
	if a.EarliestDeletionTime == nil || !a.EarliestDeletionTime.Equal(*ts("2027-01-01T00:00:00Z")) {
		t.Fatalf("a: unexpected time %+v", a)
	}
	b := resultOf(t, out, "b")
	if !b.DeletableImmediately {
		t.Fatalf("b: expected immediately deletable, got %+v", b)
	}
	if len(out.Batches) != 2 {
		t.Fatalf("expected 2 batches, got %+v", out.Batches)
	}
	if !out.Batches[0].Immediate || out.Batches[0].ObjectIDs[0] != "b" {
		t.Fatalf("batch 0 should be immediate with b: %+v", out.Batches[0])
	}
}

func TestStrongRefBlocksUntilReferrerDeletable(t *testing.T) {
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2027-06-01T00:00:00Z"), StrongRefs: []string{"b"}},
		{ID: "b", RetentionUntil: ts("2026-01-01T00:00:00Z")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	b := resultOf(t, out, "b")
	if b.EarliestDeletionTime == nil || !b.EarliestDeletionTime.Equal(*ts("2027-06-01T00:00:00Z")) {
		t.Fatalf("b must wait for a: %+v", b)
	}
	if *resultOf(t, out, "a").Batch != *b.Batch {
		t.Fatalf("a and b should share a batch")
	}
}

func TestWeakRefDoesNotBlock(t *testing.T) {
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2027-06-01T00:00:00Z"), WeakRefs: []string{"b"}},
		{ID: "b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !resultOf(t, out, "b").DeletableImmediately {
		t.Fatalf("weak ref must not block b")
	}
}

func TestCycleTreatedAsWhole(t *testing.T) {
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", RetentionUntil: ts("2026-01-01T00:00:00Z"), StrongRefs: []string{"b"}},
		{ID: "b", RetentionUntil: ts("2028-03-01T00:00:00Z"), StrongRefs: []string{"a", "d"}},
		{ID: "c", StrongRefs: []string{"a"}},
		{ID: "d"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, b := resultOf(t, out, "a"), resultOf(t, out, "b")
	want := ts("2028-03-01T00:00:00Z")
	if a.EarliestDeletionTime == nil || !a.EarliestDeletionTime.Equal(*want) {
		t.Fatalf("cycle time should be max retention: a=%+v", a)
	}
	if b.EarliestDeletionTime == nil || !b.EarliestDeletionTime.Equal(*want) {
		t.Fatalf("cycle time should be max retention: b=%+v", b)
	}
	if *a.Batch != *b.Batch {
		t.Fatalf("cycle members must share a batch")
	}
	if !resultOf(t, out, "c").DeletableImmediately {
		t.Fatalf("c only references the cycle; it is not kept alive by it")
	}
	d := resultOf(t, out, "d")
	if d.EarliestDeletionTime == nil || !d.EarliestDeletionTime.Equal(*want) {
		t.Fatalf("d is kept alive by the cycle: %+v", d)
	}
}

func TestLegalHoldPropagatesWithChain(t *testing.T) {
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", LegalHold: true, StrongRefs: []string{"b"}},
		{ID: "b", StrongRefs: []string{"c"}},
		{ID: "c"},
		{ID: "d"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		r := resultOf(t, out, id)
		if !r.PermanentlyBlocked {
			t.Fatalf("%s should be permanently blocked: %+v", id, r)
		}
		if r.Batch != nil || r.EarliestDeletionTime != nil {
			t.Fatalf("blocked object must have no batch/time: %+v", r)
		}
	}
	c := resultOf(t, out, "c")
	if len(c.BlockReasons) != 1 {
		t.Fatalf("c should have one reason chain: %+v", c.BlockReasons)
	}
	ch := c.BlockReasons[0]
	if ch.Cause != "legal-hold" || ch.RootID != "a" {
		t.Fatalf("bad chain cause: %+v", ch)
	}
	want := []string{"a", "b", "c"}
	if len(ch.Path) != len(want) {
		t.Fatalf("bad chain path: %+v", ch.Path)
	}
	for i := range want {
		if ch.Path[i] != want[i] {
			t.Fatalf("bad chain path: %+v", ch.Path)
		}
	}
	if !resultOf(t, out, "d").DeletableImmediately {
		t.Fatalf("d unaffected")
	}
}

func TestLegalHoldInsideCycleBlocksWholeCycle(t *testing.T) {
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", LegalHold: true, StrongRefs: []string{"b"}},
		{ID: "b", StrongRefs: []string{"a", "c"}},
		{ID: "c"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !resultOf(t, out, id).PermanentlyBlocked {
			t.Fatalf("%s should be blocked", id)
		}
	}
	if len(out.Batches) != 0 {
		t.Fatalf("no deletable batches expected: %+v", out.Batches)
	}
}

func TestValidationAndWarnings(t *testing.T) {
	if _, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", StrongRefs: []string{"ghost"}},
	}}); err == nil {
		t.Fatal("dangling strong ref must be an error")
	}
	if _, err := Plan(PlanInput{Objects: []ObjectInput{{ID: "a"}, {ID: "a"}}}); err == nil {
		t.Fatal("duplicate id must be an error")
	}
	out, err := Plan(PlanInput{Objects: []ObjectInput{
		{ID: "a", WeakRefs: []string{"ghost"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("expected dangling weak ref warning: %+v", out.Warnings)
	}
}
