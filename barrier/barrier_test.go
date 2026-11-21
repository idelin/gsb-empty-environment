package barrier

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, key
}

func leaf(seq uint64) Leaf {
	return Leaf{Seq: seq, Hash: sha256.Sum256([]byte{byte(seq), byte(seq >> 8)})}
}

func leaves(from, to uint64) []Leaf {
	out := make([]Leaf, 0, to-from+1)
	for s := from; s <= to; s++ {
		out = append(out, leaf(s))
	}
	return out
}

func proof(window uint64, partition string, baseRoot [32]byte, from, to uint64) PrefixProof {
	return PrefixProof{
		WindowID:  window,
		Partition: partition,
		BaseRoot:  baseRoot,
		Leaves:    leaves(from, to),
	}
}

// setup builds a coordinator at window 1 with the given partitions, each
// bounded at bound, and returns it ready for submissions.
func setup(t *testing.T, partitions []string, bound uint64) (*Coordinator, ed25519.PublicKey) {
	t.Helper()
	pub, key := newKey(t)
	c := NewCoordinator(key, 1, partitions)
	bounds := map[string]uint64{}
	for _, p := range partitions {
		bounds[p] = bound
	}
	if err := c.SetBounds(bounds); err != nil {
		t.Fatal(err)
	}
	return c, pub
}

func TestSealRequiresEveryManifestPartition(t *testing.T) {
	c, pub := setup(t, []string{"a", "b", "c"}, 4)

	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 4)); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(proof(1, "b", [32]byte{}, 1, 4)); err != nil {
		t.Fatal(err)
	}

	// Partition c is missing: the seal must fail and name it.
	_, err := c.SealWindow()
	var miss *MissingPartitionsError
	if !errors.As(err, &miss) {
		t.Fatalf("expected MissingPartitionsError, got %v", err)
	}
	if len(miss.Partitions) != 1 || miss.Partitions[0] != "c" {
		t.Fatalf("unexpected missing partitions: %v", miss.Partitions)
	}

	// A "newer root" from another partition cannot fill the gap: a
	// proof naming an unknown partition is rejected outright, and a
	// duplicate from an already-submitted partition is rejected too.
	if err := c.Submit(proof(1, "c-newer", [32]byte{}, 1, 4)); !errors.Is(err, ErrUnknownPartition) {
		t.Fatalf("expected ErrUnknownPartition, got %v", err)
	}
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 4)); !errors.Is(err, ErrDuplicateProof) {
		t.Fatalf("expected ErrDuplicateProof, got %v", err)
	}

	// Only c's own contiguous prefix proof completes the barrier.
	if err := c.Submit(proof(1, "c", [32]byte{}, 1, 4)); err != nil {
		t.Fatal(err)
	}
	seal, err := c.SealWindow()
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySeal(pub, seal) {
		t.Fatal("seal signature does not verify")
	}
	if len(seal.PartitionRoots) != 3 {
		t.Fatalf("expected 3 partition roots, got %d", len(seal.PartitionRoots))
	}
}

func TestGapBackfill(t *testing.T) {
	c, _ := setup(t, []string{"a"}, 4)

	// Gapped proof: seq 3 missing.
	gapped := PrefixProof{WindowID: 1, Partition: "a", Leaves: []Leaf{leaf(1), leaf(2), leaf(4)}}
	if err := c.Submit(gapped); !errors.Is(err, ErrGap) {
		t.Fatalf("expected ErrGap, got %v", err)
	}

	// Short proof: stops before the bound.
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 3)); !errors.Is(err, ErrGap) {
		t.Fatalf("expected ErrGap, got %v", err)
	}

	// Backfilled contiguous prefix is accepted and seals.
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SealWindow(); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateSealRejected(t *testing.T) {
	c, _ := setup(t, []string{"a"}, 2)
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 2)); err != nil {
		t.Fatal(err)
	}
	first, err := c.SealWindow()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SealWindow(); !errors.Is(err, ErrDuplicateSeal) {
		t.Fatalf("expected ErrDuplicateSeal, got %v", err)
	}
	// A sealed window accepts no further submissions.
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 2)); !errors.Is(err, ErrWindowSealed) {
		t.Fatalf("expected ErrWindowSealed, got %v", err)
	}
	// The first seal stays authoritative.
	if got := c.CurrentSeal(); got != first {
		t.Fatal("seal mutated after duplicate attempt")
	}
}

func TestWindowSwitchRejectsMixedGenerations(t *testing.T) {
	c, pub := setup(t, []string{"a", "b"}, 2)
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(proof(1, "b", [32]byte{}, 1, 2)); err != nil {
		t.Fatal(err)
	}
	seal1, err := c.SealWindow()
	if err != nil {
		t.Fatal(err)
	}

	next, err := c.AdvanceWindow()
	if err != nil {
		t.Fatal(err)
	}
	if next.WindowID != 2 {
		t.Fatalf("expected window 2, got %d", next.WindowID)
	}
	// The next window chains from the sealed roots of window 1.
	for _, id := range []string{"a", "b"} {
		spec := next.Partitions[id]
		if spec.Base != 2 || spec.BaseRoot != seal1.PartitionRoots[id] {
			t.Fatalf("partition %q not chained from previous seal: %+v", id, spec)
		}
	}
	if err := c.SetBounds(map[string]uint64{"a": 5, "b": 5}); err != nil {
		t.Fatal(err)
	}

	// A proof minted for window 1 cannot be replayed in window 2.
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 2)); !errors.Is(err, ErrWindowMismatch) {
		t.Fatalf("expected ErrWindowMismatch, got %v", err)
	}
	// A window-2 proof that skips the chain (zero base root instead of
	// the sealed window-1 root) is a different generation and rejected.
	if err := c.Submit(proof(2, "a", [32]byte{}, 3, 5)); !errors.Is(err, ErrBaseMismatch) {
		t.Fatalf("expected ErrBaseMismatch, got %v", err)
	}

	// Correctly chained proofs seal window 2.
	if err := c.Submit(proof(2, "a", seal1.PartitionRoots["a"], 3, 5)); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(proof(2, "b", seal1.PartitionRoots["b"], 3, 5)); err != nil {
		t.Fatal(err)
	}
	seal2, err := c.SealWindow()
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySeal(pub, seal2) {
		t.Fatal("window 2 seal does not verify")
	}
	if seal2.AggregateRoot == seal1.AggregateRoot {
		t.Fatal("window 2 aggregate root must differ from window 1")
	}
}

func TestMigrationTakesEffectNextWindow(t *testing.T) {
	c, _ := setup(t, []string{"a"}, 2)

	// Queue the migration: add "b", effective next window only.
	c.QueueMigration(Migration{Add: []string{"b"}})

	// In the current window "b" is still unknown.
	if err := c.Submit(proof(1, "b", [32]byte{}, 1, 2)); !errors.Is(err, ErrUnknownPartition) {
		t.Fatalf("expected ErrUnknownPartition, got %v", err)
	}

	// Current window seals with "a" alone.
	if err := c.Submit(proof(1, "a", [32]byte{}, 1, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SealWindow(); err != nil {
		t.Fatal(err)
	}

	next, err := c.AdvanceWindow()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := next.Partitions["b"]; !ok {
		t.Fatal("migrated partition missing from next window manifest")
	}
	if err := c.SetBounds(map[string]uint64{"a": 4, "b": 3}); err != nil {
		t.Fatal(err)
	}

	// The migrated partition is now mandatory: sealing without it fails.
	if err := c.Submit(proof(2, "a", c.Manifest().Partitions["a"].BaseRoot, 3, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SealWindow(); err == nil {
		t.Fatal("seal must fail while migrated partition proof is missing")
	}
	if err := c.Submit(proof(2, "b", [32]byte{}, 1, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SealWindow(); err != nil {
		t.Fatal(err)
	}
}

func TestAdvanceBeforeSealRejected(t *testing.T) {
	c, _ := setup(t, []string{"a"}, 2)
	if _, err := c.AdvanceWindow(); !errors.Is(err, ErrWindowNotSealed) {
		t.Fatalf("expected ErrWindowNotSealed, got %v", err)
	}
}
