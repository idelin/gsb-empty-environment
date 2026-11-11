package proofcache

import (
	"errors"
	"sync"
	"testing"
)

// Mutual recursion: f and g call each other. Their drafts form a cyclic
// dependency graph, and the batch validates the cycle as one fixed point.
func TestMutualRecursionFixedPoint(t *testing.T) {
	c := newTestCache()
	b, err := c.BeginBatch([]Label{"f", "g"})
	if err != nil {
		t.Fatal(err)
	}

	contractF := digestFor("contract-f")
	contractG := digestFor("contract-g")
	fDeps := []DepClaim{{Label: "g", Contract: contractG}}
	gDeps := []DepClaim{{Label: "f", Contract: contractF}}

	fFP := fpFor(t, "ir-f", fDeps)
	gFP := fpFor(t, "ir-g", gDeps)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", fDeps, gen(b, "f"))); err != nil {
			t.Errorf("put f: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := b.Put(draftFor(t, "g", "ir-g", "contract-g", gDeps, gen(b, "g"))); err != nil {
			t.Errorf("put g: %v", err)
		}
	}()
	wg.Wait()

	published := mustCommit(t, b)
	if len(published) != 2 {
		t.Fatalf("fixed point must publish both members, got %d", len(published))
	}

	entryF := assertLookup(t, c, "f", fFP)
	entryG := assertLookup(t, c, "g", gFP)
	if entryF.BatchID != entryG.BatchID {
		t.Fatal("fixed-point members must share a batch certificate id")
	}
}

// A fixed point whose internal edge disagrees with the target contract fails
// atomically: neither member is published.
func TestFixedPointRejectsInternalMismatch(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f", "g"})

	fDeps := []DepClaim{{Label: "g", Contract: digestFor("contract-g-wrong")}}
	gDeps := []DepClaim{{Label: "f", Contract: digestFor("contract-f")}}
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", fDeps, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(draftFor(t, "g", "ir-g", "contract-g", gDeps, gen(b, "g"))); err != nil {
		t.Fatal(err)
	}
	_, err := b.Commit()
	assertErrorIs(t, err, ErrDependency)

	assertLookupMisses(t, c, "f", fpFor(t, "ir-f", fDeps))
	assertLookupMisses(t, c, "g", fpFor(t, "ir-g", gDeps))
}

// A cycle mixing batch members and an external dependency checks both kinds of
// edges.
func TestFixedPointWithExternalEdge(t *testing.T) {
	c := newTestCache()

	leaf, _ := c.BeginBatch([]Label{"leaf"})
	if err := leaf.Put(draftFor(t, "leaf", "ir-leaf", "contract-leaf", nil, gen(leaf, "leaf"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, leaf)

	b, _ := c.BeginBatch([]Label{"f", "g"})
	contractF := digestFor("contract-f")
	contractG := digestFor("contract-g")
	leafContract := digestFor("contract-leaf")
	fDeps := []DepClaim{
		{Label: "g", Contract: contractG},
		{Label: "leaf", Contract: leafContract},
	}
	gDeps := []DepClaim{{Label: "f", Contract: contractF}}
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", fDeps, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(draftFor(t, "g", "ir-g", "contract-g", gDeps, gen(b, "g"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)

	assertLookup(t, c, "f", fpFor(t, "ir-f", fDeps))
	assertLookup(t, c, "g", fpFor(t, "ir-g", gDeps))

	// Changing the external leaf invalidates the whole fixed point because
	// f's closure edge goes stale.
	updated, _ := c.BeginBatch([]Label{"leaf"})
	if err := updated.Put(draftFor(t, "leaf", "ir-leaf-2", "contract-leaf-2", nil, gen(updated, "leaf"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, updated)

	_, err := c.Lookup("f", fpFor(t, "ir-f", fDeps))
	assertErrorIs(t, err, ErrDependency)
}

// Commit before every member has drafted is refused, but the batch stays open
// so the remaining worker can finish.
func TestCommitRejectsIncompleteBatch(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f", "g"})
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	_, err := b.Commit()
	assertErrorIs(t, err, ErrBatchIncomplete)
	if b.IsClosed() {
		t.Fatal("incomplete batch must remain open")
	}

	if err := b.Put(draftFor(t, "g", "ir-g", "contract-g",
		[]DepClaim{{Label: "f", Contract: digestFor("contract-f")}}, gen(b, "g"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f",
		[]DepClaim{{Label: "g", Contract: digestFor("contract-g")}}, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)
}

// A draft arriving after the label generation advanced cannot enter the batch,
// modeling a stale proof from a concurrent verification round.
func TestStaleDraftRejected(t *testing.T) {
	c := newTestCache()

	seed, _ := c.BeginBatch([]Label{"seed"})
	if err := seed.Put(draftFor(t, "seed", "ir-seed", "contract-seed", nil, gen(seed, "seed"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, seed)

	b, _ := c.BeginBatch([]Label{"f"})
	fGeneration := gen(b, "f")

	// An unrelated label committing must not fence f's batch.
	other, _ := c.BeginBatch([]Label{"other"})
	if err := other.Put(draftFor(t, "other", "ir-other", "contract-other", nil, gen(other, "other"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, other)
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, fGeneration)); err != nil {
		t.Fatalf("unrelated commit must not fence f: %v", err)
	}

	// Invalidating f advances its generation and fences the open batch.
	c.Invalidate("f")
	err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, fGeneration))
	assertFenced(t, err)
	_, err = b.Commit()
	assertFenced(t, err)

	// The host re-analyzes under the current generation in a new batch.
	retry, _ := c.BeginBatch([]Label{"f"})
	if gen(retry, "f") == fGeneration {
		t.Fatal("retry batch must observe the advanced generation")
	}
	if err := retry.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(retry, "f"))); err != nil {
		t.Fatalf("fresh batch must accept a fresh draft: %v", err)
	}
	mustCommit(t, retry)
}

// Invalidation racing an in-flight batch fences the late draft and commit.
func TestInvalidationRacesBatch(t *testing.T) {
	c := newTestCache()
	seed, _ := c.BeginBatch([]Label{"seed"})
	if err := seed.Put(draftFor(t, "seed", "ir-seed", "contract-seed", nil, gen(seed, "seed"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, seed)

	b, _ := c.BeginBatch([]Label{"f"})
	fGeneration := gen(b, "f")
	c.Invalidate("f")

	err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, fGeneration))
	assertFenced(t, err)
	if !b.IsClosed() {
		t.Fatal("invalidation must close the in-flight batch")
	}
	_, err = b.Commit()
	assertErrorIs(t, err, ErrBatchClosed)
}

// An old proof cannot be relabeled: entries are keyed by both label and
// fingerprint, and the binding covers the label and proof bytes.
func TestOldProofCannotBeRelabeled(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f"})
	fpF := fpFor(t, "ir-f", nil)
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, gen(b, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, b)

	// Identical IR content under a different label is a distinct identity; the
	// old entry cannot satisfy it.
	fpG := fpFor(t, "ir-f", nil)
	assertLookupMisses(t, c, "g", fpG)

	// Publishing g with f's proof bytes works only through a fresh draft under
	// g's own label and generation, producing a distinct binding.
	entryF := assertLookup(t, c, "f", fpF)
	gb, _ := c.BeginBatch([]Label{"g"})
	if err := gb.Put(Draft{
		Label:       "g",
		Fingerprint: fpG,
		Contract:    digestFor("contract-g"),
		Proof:       append(Proof(nil), entryF.Proof...),
		Generation:  gen(gb, "g"),
	}); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, gb)
	entryG := assertLookup(t, c, "g", fpG)
	if entryG.Binding() == entryF.Binding() {
		t.Fatal("relabeled proof must produce a different binding")
	}

	// After f's contract changes, f misses while g stays independently valid.
	updated, _ := c.BeginBatch([]Label{"f"})
	if err := updated.Put(draftFor(t, "f", "ir-f-2", "contract-f-2", nil, gen(updated, "f"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, updated)
	assertLookupMisses(t, c, "f", fpF)
	assertLookup(t, c, "g", fpG)
}

// Once a member label is invalidated, replacing an earlier draft is refused
// even though the batch still holds an older draft for it.
func TestPutReplacementFencing(t *testing.T) {
	c := newTestCache()
	b, _ := c.BeginBatch([]Label{"f"})
	fGeneration := gen(b, "f")
	if err := b.Put(draftFor(t, "f", "ir-f", "contract-f", nil, fGeneration)); err != nil {
		t.Fatal(err)
	}

	c.Invalidate("f")
	err := b.Put(draftFor(t, "f", "ir-f-new", "contract-f", nil, fGeneration))
	assertFenced(t, err)
}

// Domain separation means identical payloads under different tags never share
// a digest, and digests are stable and hex-round-trippable.
func TestDigestDomainSeparation(t *testing.T) {
	a := NewHasher()
	a.WriteString("ir", "same")
	hb := NewHasher()
	hb.WriteString("rule", "same")
	if a.Sum() == hb.Sum() {
		t.Fatal("identical payloads under different tags must not collide")
	}

	d := digestFor("stable")
	parsed, err := ParseDigest(d.String())
	if err != nil || parsed != d {
		t.Fatalf("digest hex round trip failed: %v", err)
	}
	if _, err := ParseDigest("abc"); !errors.Is(err, ErrBadDigest) {
		t.Fatalf("short digest must fail with ErrBadDigest, got %v", err)
	}
}
