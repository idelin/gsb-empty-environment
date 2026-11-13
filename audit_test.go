package netsec

import (
	"strings"
	"testing"
)

func TestSafePolicyHasNoStaleStartWitness(t *testing.T) {
	if w, ok := FindShortestStaleStartWitness(SafeAuditPolicy(), 12); ok {
		t.Fatalf("safe implementation produced a witness: %s", w)
	}
}

func TestLegacyPolicyShortestWitness(t *testing.T) {
	w, ok := FindShortestStaleStartWitness(LegacyUnsafeAuditPolicy(), 10)
	if !ok {
		t.Fatal("unsafe implementation produced no stale-start witness")
	}
	if len(w.Steps) != 6 {
		t.Fatalf("witness length = %d, want shortest 6: %s", len(w.Steps), w)
	}
	want := "write -> round.snapshot -> write -> dirty.ack -> freeze.final -> target.start"
	if got := w.ActionSequence(); got != want {
		t.Fatalf("witness = %s, want %s", got, want)
	}
	if w.SourceFreezeGeneration != 2 || w.TargetStartupGeneration != 1 {
		t.Fatalf("witness generations = target %d, freeze %d", w.TargetStartupGeneration, w.SourceFreezeGeneration)
	}
	if !strings.Contains(w.String(), "generation 1") || !strings.Contains(w.String(), "froze at 2") {
		t.Fatalf("witness rendering = %s", w.String())
	}

	// Removing either half of the bug is enough to make the witness vanish.
	onlyClear := LegacyUnsafeAuditPolicy()
	onlyClear.StartWithoutGenerationCheck = false
	if w, ok := FindShortestStaleStartWitness(onlyClear, 10); ok {
		t.Fatalf("manifest gate alone produced witness: %s", w)
	}
	onlyStart := LegacyUnsafeAuditPolicy()
	onlyStart.ClearDirtyIgnoresGeneration = false
	if w, ok := FindShortestStaleStartWitness(onlyStart, 10); ok {
		t.Fatalf("generation-aware dirty clearing alone produced witness: %s", w)
	}
}

func TestFreezeSkipDirtyCopyWitness(t *testing.T) {
	p := LegacyUnsafeAuditPolicy()
	p.ClearDirtyIgnoresGeneration = false
	p.FreezeSkipsDirtyCopy = true
	w, ok := FindShortestStaleStartWitness(p, 10)
	if !ok {
		t.Fatal("no witness for missing final dirty copy")
	}
	if w.TargetStartupGeneration >= w.SourceFreezeGeneration {
		t.Fatalf("not a stale witness: %s", w)
	}
	if len(w.Steps) > 6 {
		t.Fatalf("expected a compact witness, got %s", w)
	}
}
