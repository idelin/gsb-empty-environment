package netsec_test

import (
	"testing"

	netsec "example.org/netsec/q0444"
)

func TestManifestCanonicalizesInputs(t *testing.T) {
	a := netsec.NewManifest("ind-2", "ind-1", "ind-2", "", "ind-3")
	b := netsec.NewManifest("ind-3", "ind-1", "ind-2")

	if a.Digest() != b.Digest() {
		t.Fatalf("same input sets produced different digests: %q vs %q", a.Digest(), b.Digest())
	}
	want := []string{"ind-1", "ind-2", "ind-3"}
	got := a.Inputs()
	if len(got) != len(want) {
		t.Fatalf("inputs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inputs = %v, want sorted/deduped %v", got, want)
		}
	}
	if !a.Contains("ind-2") || a.Contains("ind-9") || a.Contains("") {
		t.Fatalf("Contains gave unexpected result for %v", a.Inputs())
	}

	// A different set must produce a different digest, and returned slices are
	// copies that cannot mutate the manifest.
	c := netsec.NewManifest("ind-1", "ind-2")
	if c.Digest() == a.Digest() {
		t.Fatal("manifests with different inputs share a digest")
	}
	inputs := c.Inputs()
	inputs[0] = "tampered"
	if c.Inputs()[0] != "ind-1" {
		t.Fatal("manifest inputs are not defensively copied")
	}

	if netsec.NewManifest().Digest() == "" {
		t.Fatal("empty manifest must still have a defined digest")
	}
}

func TestDeletionLogGenerationsAndSince(t *testing.T) {
	clock := &fakeClock{}
	deletes := netsec.NewDeletionLog(netsec.WithClock(clock.now))

	if deletes.CurrentGeneration() != 0 {
		t.Fatalf("fresh log generation = %d, want 0", deletes.CurrentGeneration())
	}

	first := deletes.Record("ind-a")
	second := deletes.Record("ind-b")
	if first.Generation != 1 || second.Generation != 2 {
		t.Fatalf("generations = %d,%d, want 1,2", first.Generation, second.Generation)
	}
	if !first.OccurredAt.Before(second.OccurredAt) {
		t.Fatalf("timestamps not ordered: %v vs %v", first.OccurredAt, second.OccurredAt)
	}

	// Re-recording the same individual is idempotent and mints no generation.
	dup := deletes.Record("ind-a")
	if dup.Generation != 1 || deletes.CurrentGeneration() != 2 {
		t.Fatalf("duplicate deletion changed generation: %+v current=%d", dup, deletes.CurrentGeneration())
	}

	sinceOne := deletes.Since(1)
	if len(sinceOne) != 1 || sinceOne[0].IndividualID != "ind-b" {
		t.Fatalf("Since(1) = %+v, want only ind-b", sinceOne)
	}
	if len(deletes.Since(2)) != 0 {
		t.Fatalf("Since(current) must be empty, got %+v", deletes.Since(2))
	}

	if !deletes.IsDeleted("ind-a") || deletes.IsDeleted("ind-z") {
		t.Fatal("IsDeleted returned inconsistent results")
	}

	events := deletes.Events()
	events[0].IndividualID = "tampered"
	if deletes.Events()[0].IndividualID != "ind-a" {
		t.Fatal("deletion events are not defensively copied")
	}
}
