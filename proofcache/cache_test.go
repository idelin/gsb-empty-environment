package proofcache

import (
	"errors"
	"sync"
	"testing"
)

// A single-member batch is the acyclic base case: publish then look up.
func TestSinglePublishAndLookup(t *testing.T) {
	c := newTestCache()
	b, err := c.BeginBatch([]Label{"f"})
	if err != nil {
		t.Fatal(err)
	}
	fp := fpFor(t, "ir-f", nil)
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	published := mustCommit(t, b)
	if len(published) != 1 || published[0].Label != "f" {
		t.Fatalf("unexpected published set: %+v", published)
	}

	entry := assertLookup(t, c, "f", fp)
	if entry.Binding().IsZero() {
		t.Fatal("entry binding must not be zero")
	}
	if string(entry.Proof) != "proof-f" {
		t.Fatalf("proof payload altered: %q", entry.Proof)
	}
	if got := c.Stats(); got.Entries != 1 || got.OpenBatches != 0 {
		t.Fatalf("unexpected stats: %+v", got)
	}
}

// Local IR change produces a different fingerprint, so the old entry misses.
func TestLocalIRChangeInvalidates(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f"})
	oldFP := fpFor(t, "ir-f", nil)
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)
	assertLookup(t, c, "f", oldFP)

	newFP := fpFor(t, "ir-f-v2", nil)
	assertLookupMisses(t, c, "f", newFP)
}

// A callee contract change must invalidate the caller even when the caller IR
// digest is untouched.
func TestDependencyContractChangeInvalidatesCaller(t *testing.T) {
	c := newTestCache()

	leafBatch, _ := c.BeginBatch([]Label{"leaf"})
	if err := leafBatch.Put(draftFor(t, "leaf", "ir-leaf", "contract-leaf", nil, gen(leafBatch, "leaf"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, leafBatch)

	callerBatch, _ := c.BeginBatch([]Label{"caller"})
	callerDeps := []DepClaim{{Label: "leaf", Contract: digestFor("contract-leaf")}}
	callerFP := fpFor(t, "ir-caller", callerDeps)
	if err := callerBatch.Put(draftFor(t, "caller", "ir-caller", "contract-caller", callerDeps, gen(callerBatch, "caller"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, callerBatch)
	assertLookup(t, c, "caller", callerFP)

	// Re-publish leaf with a new contract; the caller now has a dangling edge.
	updatedLeaf, _ := c.BeginBatch([]Label{"leaf"})
	if err := updatedLeaf.Put(draftFor(t, "leaf", "ir-leaf-v2", "contract-leaf-v2", nil, gen(updatedLeaf, "leaf"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, updatedLeaf)

	_, err := c.Lookup("caller", callerFP)
	assertErrorIs(t, err, ErrDependency)

	// A fresh caller fingerprint claiming the new contract validates again.
	repairedDeps := []DepClaim{{Label: "leaf", Contract: digestFor("contract-leaf-v2")}}
	repairedFP := fpFor(t, "ir-caller", repairedDeps)
	repaired, _ := c.BeginBatch([]Label{"caller"})
	if err := repaired.Put(draftFor(t, "caller", "ir-caller", "contract-caller-v2", repairedDeps, gen(repaired, "caller"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, repaired)
	assertLookup(t, c, "caller", repairedFP)
}

// Rule version gating: fingerprints carry the rule version and a cache pinned
// to a newer rule set rejects the old fingerprint.
func TestRuleVersionGating(t *testing.T) {
	c := New(testRule)
	b, _ := c.BeginBatch([]Label{"f"})
	fp := fpFor(t, "ir-f", nil)
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)
	assertLookup(t, c, "f", fp)

	oldRuleFP, err := NewFingerprint(digestFor("ir-f"), nil, RuleVersion{Name: "taint", Version: "v0.9.0"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Lookup("f", oldRuleFP)
	assertErrorIs(t, err, ErrRuleVersion)

	// A draft built under the wrong rule version cannot enter a batch.
	wrong, _ := c.BeginBatch([]Label{"g"})
	err = wrong.Put(Draft{
		Label:       "g",
		Fingerprint: oldRuleFP,
		Contract:    digestFor("contract-g"),
		Proof:       Proof("p"),
		Generation:  gen(wrong, "g"),
	})
	assertErrorIs(t, err, ErrRuleVersion)
}

// Fingerprints canonicalize dependency order and reject contradictory claims.
func TestFingerprintCanonicalization(t *testing.T) {
	depsA := []DepClaim{
		{Label: "b", Contract: digestFor("c-b")},
		{Label: "a", Contract: digestFor("c-a")},
	}
	depsB := []DepClaim{
		{Label: "a", Contract: digestFor("c-a")},
		{Label: "b", Contract: digestFor("c-b")},
	}
	fpA, err := NewFingerprint(digestFor("ir"), depsA, testRule)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := NewFingerprint(digestFor("ir"), depsB, testRule)
	if err != nil {
		t.Fatal(err)
	}
	if fpA.Sum() != fpB.Sum() {
		t.Fatal("reordered identical deps must canonicalize to one fingerprint")
	}

	conflicting := []DepClaim{
		{Label: "a", Contract: digestFor("c-a")},
		{Label: "a", Contract: digestFor("c-other")},
	}
	if _, err := NewFingerprint(digestFor("ir"), conflicting, testRule); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("conflicting deps must be rejected, got %v", err)
	}

	duplicated := []DepClaim{
		{Label: "a", Contract: digestFor("c-a")},
		{Label: "a", Contract: digestFor("c-a")},
	}
	fpDup, err := NewFingerprint(digestFor("ir"), duplicated, testRule)
	if err != nil {
		t.Fatal(err)
	}
	if len(fpDup.Deps) != 1 {
		t.Fatalf("duplicate deps must collapse, got %d edges", len(fpDup.Deps))
	}
}

// A draft cannot reference a dependency with no published contract.
func TestCommitRejectsMissingExternalDependency(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"caller"})
	deps := []DepClaim{{Label: "ghost", Contract: digestFor("contract-ghost")}}
	err := b.Put(draftFor(t, "caller", "ir-caller", "contract-caller", deps, gen(b, "caller")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Commit()
	assertErrorIs(t, err, ErrDependency)
	if !b.IsClosed() {
		t.Fatal("failed batch must be closed")
	}
	if c.Stats().Entries != 0 {
		t.Fatal("failed commit must not publish anything")
	}
}

// Aborted drafts never become visible.
func TestAbortPublishesNothing(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f"})
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	b.Abort()
	if _, err := b.Commit(); !errors.Is(err, ErrBatchClosed) {
		t.Fatalf("aborted batch must reject commit, got %v", err)
	}
	fp := fpFor(t, "ir-f", nil)
	assertLookupMisses(t, c, "f", fp)

	next, err := c.BeginBatch([]Label{"f"})
	if err != nil {
		t.Fatalf("label must be reusable after abort: %v", err)
	}
	if err := next.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(next, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, next)
	assertLookup(t, c, "f", fp)
}

// Concurrent batches cannot claim the same label.
func TestOverlappingBatchesRejected(t *testing.T) {
	c := newTestCache()
	first, err := c.BeginBatch([]Label{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.BeginBatch([]Label{"b", "c"})
	assertErrorIs(t, err, ErrBatchConflict)
	first.Abort()
	if _, err := c.BeginBatch([]Label{"b", "c"}); err != nil {
		t.Fatalf("label must be free after abort: %v", err)
	}
}

// Returned entries are copies: mutating them cannot corrupt the cache.
func TestReturnedEntriesAreCopies(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f"})
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)
	fp := fpFor(t, "ir-f", nil)

	first := assertLookup(t, c, "f", fp)
	first.Proof[0] = 'X'
	first.Deps = append(first.Deps, DepClaim{Label: "evil", Contract: digestFor("x")})
	second := assertLookup(t, c, "f", fp)
	if string(second.Proof) != "proof-f" {
		t.Fatal("proof payload was corrupted through a returned copy")
	}
	if len(second.Deps) != 0 {
		t.Fatal("dependency list was corrupted through a returned copy")
	}
}

// Invalidate removes entries and fences in-flight batches for the same label,
// while leaving unrelated labels untouched.
func TestInvalidateRemovesAndFences(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f"})
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)
	oldEvent := c.Epoch()

	open, _ := c.BeginBatch([]Label{"g"})
	openGen := gen(open, "g")
	if got := c.Invalidate("f", "unknown"); got != 1 {
		t.Fatalf("one entry should be removed, got %d", got)
	}
	if c.Epoch() != oldEvent+1 {
		t.Fatal("invalidation must advance the event counter")
	}

	// g is unrelated, so its generation is unchanged and its batch still works.
	if gen(open, "g") != openGen {
		t.Fatal("invalidation of f must not fence an unrelated g batch")
	}
	if err := open.Put(draftFor(t, "g", "ir-g", "contract-g", nil, openGen)); err != nil {
		t.Fatalf("unrelated batch must remain valid: %v", err)
	}
	mustCommit(t, open)

	// A fresh batch touching the invalidated label sees the new generation; a
	// draft carrying the old one is fenced.
	fenced, _ := c.BeginBatch([]Label{"f"})
	err := fenced.Put(draftFor(t, "f", "ir-f", "contract-f", nil, openGen))
	assertErrorIs(t, err, ErrStale)
	// The stale draft is rejected, so nothing is stored and the batch cannot
	// complete; the host must open a new batch under the current generation.
	_, err = fenced.Commit()
	assertErrorIs(t, err, ErrBatchIncomplete)
}

// Invalidation closes the in-flight batch for the label and frees it for a
// replacement batch immediately.
func TestInvalidateClosesInFlightBatch(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f", "h"})
	c.Invalidate("f")

	if !b.IsClosed() {
		t.Fatal("batch touching an invalidated label must be closed")
	}
	if _, err := b.Commit(); !errors.Is(err, ErrBatchClosed) {
		t.Fatalf("closed batch must reject commit, got %v", err)
	}

	// Both member labels are released.
	retry, err := c.BeginBatch([]Label{"f", "h"})
	if err != nil {
		t.Fatalf("labels must be reusable after invalidation: %v", err)
	}
	if err := retry.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(retry, "f"))); err != nil {
		t.Fatal(err)
	}
	if err := retry.Put(draftFor(t, "h", "ir-h", "contract-h", nil, gen(retry, "h"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, retry)
}

// Concurrent readers and writers must never observe a partial batch or a torn
// entry. Writers operate on distinct labels so per-label generations do not
// fence one another; readers hammer a published fixed point.
func TestConcurrentReadersAndWriters(t *testing.T) {
	c := newTestCache()

	leaf, _ := c.BeginBatch([]Label{"leaf"})
	if err := leaf.Put(draftFor(t, "leaf", "ir-leaf", "contract-leaf", nil, gen(leaf, "leaf"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, leaf)

	cycle, _ := c.BeginBatch([]Label{"f", "g"})
	contractF := digestFor("contract-f")
	contractG := digestFor("contract-g")
	fDeps := []DepClaim{{Label: "g", Contract: contractG}}
	gDeps := []DepClaim{{Label: "f", Contract: contractF}}
	if err := cycle.Put(draftFor(t, "f", "ir-f", "contract-f", fDeps, gen(cycle, "f"))); err != nil {
		t.Fatal(err)
	}
	if err := cycle.Put(draftFor(t, "g", "ir-g", "contract-g", gDeps, gen(cycle, "g"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, cycle)
	fFP := fpFor(t, "ir-f", fDeps)
	gFP := fpFor(t, "ir-g", gDeps)

	var wg sync.WaitGroup
	const writers = 8
	for i := 0; i < writers; i++ {
		label := Label("indep-" + string(rune('a'+i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := c.BeginBatch([]Label{label})
			if err != nil {
				t.Errorf("BeginBatch: %v", err)
				return
			}
			if err := b.Put(draftFor(t, label, "ir-"+string(label), "contract-"+string(label), nil, gen(b, label))); err != nil {
				t.Errorf("Put: %v", err)
				return
			}
			if _, err := b.Commit(); err != nil {
				t.Errorf("Commit: %v", err)
			}
		}()
	}
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Lookup("f", fFP); err != nil {
				t.Errorf("lookup f: %v", err)
			}
			if _, err := c.Lookup("g", gFP); err != nil {
				t.Errorf("lookup g: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := c.Stats(); got.Entries != writers+3 {
		t.Fatalf("want %d entries, got %d", writers+3, got.Entries)
	}
	assertLookup(t, c, "f", fFP)
	assertLookup(t, c, "g", gFP)
}
