package evidence

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

const (
	logA = "log-sha256-aaaaaaaa"
	logB = "log-sha256-bbbbbbbb"
)

var errFake = errors.New("fake verifier rejection")

// fakeVerifier models an injected verifier. A head whose signature is
// badSig fails; a proof carrying a bad hash fails. Everything else is
// accepted, letting the tests focus on graph merging rather than crypto.
type fakeVerifier struct {
	badSig  []byte
	badHash []byte
}

func (f fakeVerifier) VerifyTreeHead(th TreeHead) error {
	if slices.Equal(th.Signature, f.badSig) {
		return errFake
	}
	return nil
}

func (f fakeVerifier) VerifyConsistency(p ConsistencyProof) error {
	for _, h := range p.Hashes {
		if slices.Equal(h, f.badHash) {
			return errFake
		}
	}
	return nil
}

func mkHead(logID string, size uint64, root byte) TreeHead {
	return TreeHead{
		LogID:     logID,
		Size:      size,
		Root:      []byte{root},
		Signature: []byte("sig-" + fmt.Sprint(size) + "-" + string(rune('a'+root-1))),
	}
}

func mkProof(p ConsistencyProof) ConsistencyProof {
	if p.Hashes == nil {
		p.Hashes = [][]byte{[]byte("proof-hash")}
	}
	return p
}

func mustAddHead(t *testing.T, m *Merger, th TreeHead) {
	t.Helper()
	if err := m.AddTreeHead(th); err != nil {
		t.Fatalf("AddTreeHead(size=%d,root=%v) unexpected error: %v", th.Size, th.Root, err)
	}
}

func mustAddProof(t *testing.T, m *Merger, p ConsistencyProof) {
	t.Helper()
	if err := m.AddConsistencyProof(p); err != nil {
		t.Fatalf("AddConsistencyProof(%d->%d) unexpected error: %v", p.OldSize, p.NewSize, err)
	}
}

func chainSizes(chain []ConsistencyProof) []uint64 {
	sizes := make([]uint64, 0, len(chain))
	for _, p := range chain {
		sizes = append(sizes, p.NewSize)
	}
	return sizes
}

// buildSharedCheckpointGraph builds:
//
//	h1(1,r1) -> h2(2,r2) -> h3a(3,r3) -> h5(5,r5)
//	                      \-> h3b(3,r3b)
//
// h3a and h3b are equal-size, different-root heads sharing the intermediate
// checkpoint h2. Edges h1->h2 and h2->h3a give an alternative longer path to
// h5, so shortest-chain selection is observable.
func buildSharedCheckpointGraph(t *testing.T, m *Merger) map[string]TreeHead {
	t.Helper()
	heads := map[string]TreeHead{
		"h1":  mkHead(logA, 1, 1),
		"h2":  mkHead(logA, 2, 2),
		"h3a": mkHead(logA, 3, 3),
		"h3b": mkHead(logA, 3, 33),
		"h5":  mkHead(logA, 5, 5),
	}
	for _, name := range []string{"h1", "h2", "h3a", "h3b", "h5"} {
		mustAddHead(t, m, heads[name])
	}
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: heads["h1"].Root,
		NewSize: 2, NewRoot: heads["h2"].Root,
	}))
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 2, OldRoot: heads["h2"].Root,
		NewSize: 3, NewRoot: heads["h3a"].Root,
	}))
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 2, OldRoot: heads["h2"].Root,
		NewSize: 3, NewRoot: heads["h3b"].Root,
	}))
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 3, OldRoot: heads["h3a"].Root,
		NewSize: 5, NewRoot: heads["h5"].Root,
	}))
	return heads
}

func TestAddTreeHeadVerifiesAndDedupes(t *testing.T) {
	verifier := fakeVerifier{badSig: []byte("forged")}
	m := New(verifier)

	good := mkHead(logA, 1, 1)
	mustAddHead(t, m, good)
	if !m.Known(good) {
		t.Fatal("verified head should be known")
	}

	// Re-adding the same head is an idempotent no-op.
	mustAddHead(t, m, good)
	if got := m.Stats().TreeHeads; got != 1 {
		t.Fatalf("TreeHeads = %d, want 1 (deduped)", got)
	}

	forged := TreeHead{LogID: logA, Size: 2, Root: []byte{2}, Signature: verifier.badSig}
	err := m.AddTreeHead(forged)
	if !errors.Is(err, ErrUntrusted) {
		t.Fatalf("forged head error = %v, want ErrUntrusted", err)
	}
	if m.Known(forged) {
		t.Fatal("rejected head must not enter the graph")
	}

	bad := TreeHead{LogID: "", Size: 0}
	if err := m.AddTreeHead(bad); !errors.Is(err, ErrInvalidTreeHead) {
		t.Fatalf("malformed head error = %v, want ErrInvalidTreeHead", err)
	}

	stats := m.Stats()
	if stats.RejectedHeads != 1 {
		t.Fatalf("RejectedHeads = %d, want 1", stats.RejectedHeads)
	}
}

func TestSameSizeDifferentRootIsWitness(t *testing.T) {
	m := New(fakeVerifier{})
	heads := buildSharedCheckpointGraph(t, m)

	rel := m.Relate(heads["h3a"], heads["h3b"])
	if rel.Status != StatusWitness {
		t.Fatalf("rel status = %s, want witness", rel.Status)
	}
	if len(rel.Chain) != 0 {
		t.Fatalf("witness must not carry a proof chain, got %d proofs", len(rel.Chain))
	}

	witnesses := m.Witnesses()
	if len(witnesses) != 1 {
		t.Fatalf("Witnesses = %d, want 1", len(witnesses))
	}
	w := witnesses[0]
	if w.LogID != logA || w.Size != 3 {
		t.Fatalf("witness = %+v, want logA size 3", w)
	}
	if string(w.A.Root) == string(w.B.Root) {
		t.Fatal("witness roots must differ")
	}
}

func TestShortestChainThroughSharedCheckpoint(t *testing.T) {
	m := New(fakeVerifier{})
	heads := buildSharedCheckpointGraph(t, m)

	// Add a direct long edge h1->h5 to create a competing longer route.
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: heads["h1"].Root,
		NewSize: 5, NewRoot: heads["h5"].Root,
	}))

	chain := m.ShortestChain(heads["h1"], heads["h5"])
	if chain == nil {
		t.Fatal("expected a proof chain from h1 to h5")
	}
	if got := chainSizes(chain); !slices.Equal(got, []uint64{5}) {
		t.Fatalf("shortest chain new sizes = %v, want [5] (direct edge)", got)
	}

	// Without the direct edge, the shortest route is h1->h2->h3a->h5.
	m2 := New(fakeVerifier{})
	h2 := buildSharedCheckpointGraph(t, m2)
	rel := m2.Relate(h2["h1"], h2["h5"])
	if rel.Status != StatusCompatible {
		t.Fatalf("rel status = %s, want compatible", rel.Status)
	}
	if got := chainSizes(rel.Chain); !slices.Equal(got, []uint64{2, 3, 5}) {
		t.Fatalf("chain new sizes = %v, want [2 3 5]", got)
	}

	// Every hop must be contiguous and directed small -> large.
	prevSize := heads["h1"].Size
	for _, hop := range rel.Chain {
		if hop.OldSize != prevSize {
			t.Fatalf("hop gap: old=%d previous new=%d", hop.OldSize, prevSize)
		}
		prevSize = hop.NewSize
	}

	// Same head compares as compatible with an empty chain.
	self := m2.Relate(h2["h2"], h2["h2"])
	if self.Status != StatusCompatible || len(self.Chain) != 0 {
		t.Fatalf("self relation = %s chain=%d, want compatible empty", self.Status, len(self.Chain))
	}
}

func TestDisconnectedHeadsArePendingNotMalicious(t *testing.T) {
	m := New(fakeVerifier{})
	heads := buildSharedCheckpointGraph(t, m)

	// h6 belongs to the same log but has no proof linking it to the rest.
	h6 := mkHead(logA, 6, 6)
	mustAddHead(t, m, h6)

	rel := m.Relate(heads["h1"], h6)
	if rel.Status != StatusPending {
		t.Fatalf("rel status = %s, want pending", rel.Status)
	}
	if rel.Chain != nil {
		t.Fatalf("pending relation must not carry a chain, got %d", len(rel.Chain))
	}

	// Argument order must not change the classification.
	if reverse := m.Relate(h6, heads["h1"]); reverse.Status != StatusPending {
		t.Fatalf("reverse rel status = %s, want pending", reverse.Status)
	}

	if chain := m.ShortestChain(heads["h1"], h6); chain != nil {
		t.Fatalf("ShortestChain = %v, want nil while disconnected", chain)
	}

	pending := m.PendingPairs()
	found := false
	for _, p := range pending {
		if p.LogID == logA && p.Smaller.Size == 1 && p.Larger.Size == 6 {
			found = true
		}
	}
	if !found {
		t.Fatal("PendingPairs must include the disconnected (h1,h6) pair")
	}

	// Connecting the pair upgrades it to compatible.
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 5, OldRoot: heads["h5"].Root,
		NewSize: 6, NewRoot: h6.Root,
	}))
	if got := m.Relate(heads["h1"], h6).Status; got != StatusCompatible {
		t.Fatalf("after connecting proof status = %s, want compatible", got)
	}
}

func TestBadProofIsolated(t *testing.T) {
	verifier := fakeVerifier{badHash: []byte("poison")}
	m := New(verifier)
	heads := buildSharedCheckpointGraph(t, m)

	// A bad proof claiming a shortcut h1->h5 must be rejected.
	bad := mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: heads["h1"].Root,
		NewSize: 5, NewRoot: heads["h5"].Root,
		Hashes: [][]byte{verifier.badHash},
	})
	err := m.AddConsistencyProof(bad)
	if !errors.Is(err, ErrBadProof) {
		t.Fatalf("bad proof error = %v, want ErrBadProof", err)
	}

	// The graph stays intact: the honest multi-hop chain still resolves.
	rel := m.Relate(heads["h1"], heads["h5"])
	if rel.Status != StatusCompatible {
		t.Fatalf("status after bad proof = %s, want compatible via honest chain", rel.Status)
	}
	if got := chainSizes(rel.Chain); !slices.Equal(got, []uint64{2, 3, 5}) {
		t.Fatalf("chain = %v, want honest [2 3 5], bad proof must not shortcut", got)
	}
	if m.Stats().BadProofs != 1 {
		t.Fatalf("BadProofs = %d, want 1", m.Stats().BadProofs)
	}

	// A valid proof for the same pair is still accepted afterwards.
	good := mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: heads["h1"].Root,
		NewSize: 5, NewRoot: heads["h5"].Root,
	})
	mustAddProof(t, m, good)
	if got := chainSizes(m.ShortestChain(heads["h1"], heads["h5"])); !slices.Equal(got, []uint64{5}) {
		t.Fatalf("chain after good proof = %v, want direct [5]", got)
	}
}

func TestBufferedBadProofIsolatedOnActivation(t *testing.T) {
	verifier := fakeVerifier{badHash: []byte("poison")}
	m := New(verifier)
	h1 := mkHead(logA, 1, 1)
	h3 := mkHead(logA, 3, 3)
	mustAddHead(t, m, h1)

	// h3 unknown: the proof buffers instead of verifying immediately.
	buffered := mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: h1.Root, NewSize: 3, NewRoot: h3.Root,
		Hashes: [][]byte{verifier.badHash},
	})
	if err := m.AddConsistencyProof(buffered); err != nil {
		t.Fatalf("buffering unknown-endpoint proof: %v", err)
	}

	// Adding the missing head triggers verification; the bad proof is dropped.
	mustAddHead(t, m, h3)
	if m.Stats().BadProofs != 1 || m.Stats().PendingProofs != 0 {
		t.Fatalf("stats = %+v, want 1 bad proof and 0 pending", m.Stats())
	}
	if got := m.Relate(h1, h3).Status; got != StatusPending {
		t.Fatalf("status = %s, want pending after buffered bad proof dropped", got)
	}
}

func TestProofsCannotCrossLogIdentities(t *testing.T) {
	m := New(fakeVerifier{})

	// Distinct root content across logs lets a cross-identity proof be
	// detected by the content it references.
	a1 := mkHead(logA, 1, 1)
	a2 := mkHead(logA, 2, 2)
	b1 := mkHead(logB, 1, 11)
	b2 := mkHead(logB, 2, 12)
	for _, th := range []TreeHead{a1, a2, b1, b2} {
		mustAddHead(t, m, th)
	}

	// logA proof reaching into logB-only content (b2) is a cross-identity link.
	cross := mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: a1.Root, NewSize: 2, NewRoot: b2.Root,
	})
	if err := m.AddConsistencyProof(cross); !errors.Is(err, ErrDifferentLog) {
		t.Fatalf("cross-log proof error = %v, want ErrDifferentLog", err)
	}

	// The mirrored direction (logB tag, logA-only content) fails too.
	crossB := mkProof(ConsistencyProof{
		LogID: logB, OldSize: 1, OldRoot: b1.Root, NewSize: 2, NewRoot: a2.Root,
	})
	if err := m.AddConsistencyProof(crossB); !errors.Is(err, ErrDifferentLog) {
		t.Fatalf("cross-log proof (logB tag) error = %v, want ErrDifferentLog", err)
	}

	if got := m.Stats().CrossLogProofs; got != 2 {
		t.Fatalf("CrossLogProofs = %d, want 2", got)
	}

	// Even identical size+root across two logs cannot be chained: node
	// identity is namespaced per log, so cross-log relations stay unknown.
	sharedA := mkHead(logA, 4, 4)
	sharedB := mkHead(logB, 4, 4)
	mustAddHead(t, m, sharedA)
	mustAddHead(t, m, sharedB)
	if got := m.Relate(sharedA, sharedB).Status; got != StatusUnknown {
		t.Fatalf("cross-log equal-size/equal-root status = %s, want unknown", got)
	}
	mustAddProof(t, m, mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: a1.Root, NewSize: 2, NewRoot: a2.Root,
	}))
	if got := m.Relate(a1, a2).Status; got != StatusCompatible {
		t.Fatalf("logA internal status = %s, want compatible", got)
	}
	if got := m.Relate(b1, b2).Status; got != StatusPending {
		t.Fatalf("logB must stay isolated: status = %s, want pending", got)
	}
}

func TestBufferedCrossLogProofIsolatedOnActivation(t *testing.T) {
	m := New(fakeVerifier{})
	a1 := mkHead(logA, 1, 7)
	mustAddHead(t, m, a1)

	// Buffer a proof whose new endpoint content does not exist yet.
	pending := mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: a1.Root, NewSize: 4, NewRoot: []byte{44},
	})
	if err := m.AddConsistencyProof(pending); err != nil {
		t.Fatalf("buffer proof: %v", err)
	}

	// The arriving head belongs to another log and owns the referenced
	// (size,root) content, so activation must classify the proof as cross-log.
	b4 := mkHead(logB, 4, 44)
	mustAddHead(t, m, b4)
	if m.Stats().CrossLogProofs != 1 {
		t.Fatalf("CrossLogProofs = %d, want 1", m.Stats().CrossLogProofs)
	}
	if m.Stats().PendingProofs != 0 {
		t.Fatalf("PendingProofs = %d, want 0 after activation", m.Stats().PendingProofs)
	}

	// A later logA head with different content gets no proof either.
	a4 := mkHead(logA, 4, 41)
	mustAddHead(t, m, a4)
	// logA never received a valid proof for its own head.
	if got := m.Relate(a1, a4).Status; got != StatusPending {
		t.Fatalf("status = %s, want pending: cross-log proof must not link logA", got)
	}
}

func TestProofDedupAndIncrementalOrdering(t *testing.T) {
	m := New(fakeVerifier{})
	h1 := mkHead(logA, 1, 1)
	h2 := mkHead(logA, 2, 2)

	proof := mkProof(ConsistencyProof{
		LogID: logA, OldSize: 1, OldRoot: h1.Root, NewSize: 2, NewRoot: h2.Root,
	})

	// Proofs arriving before both endpoints are buffered and deduplicated.
	mustAddProof(t, m, proof)
	if err := m.AddConsistencyProof(proof); err != nil {
		t.Fatalf("duplicate buffered proof: %v", err)
	}
	mustAddHead(t, m, h1)
	mustAddHead(t, m, h2)

	stats := m.Stats()
	if stats.Proofs != 1 {
		t.Fatalf("Proofs = %d, want 1 after activation", stats.Proofs)
	}
	if stats.DuplicateProofs != 1 {
		t.Fatalf("DuplicateProofs = %d, want 1", stats.DuplicateProofs)
	}
	if stats.PendingProofs != 0 {
		t.Fatalf("PendingProofs = %d, want 0", stats.PendingProofs)
	}

	// Re-adding after activation is still a no-op.
	if err := m.AddConsistencyProof(proof); err != nil {
		t.Fatalf("duplicate active proof: %v", err)
	}
	if got := m.Stats().Proofs; got != 1 {
		t.Fatalf("Proofs = %d, still want 1", got)
	}
	if got := m.Relate(h1, h2).Status; got != StatusCompatible {
		t.Fatalf("status = %s, want compatible", got)
	}

	// Malformed proofs never buffer.
	malformed := ConsistencyProof{LogID: logA, OldSize: 5, NewSize: 5, OldRoot: []byte{1}, NewRoot: []byte{2}}
	if err := m.AddConsistencyProof(malformed); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("malformed proof error = %v, want ErrInvalidProof", err)
	}
}
