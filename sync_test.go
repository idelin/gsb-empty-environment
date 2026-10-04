package antientropy

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestSyncEmptyStores(t *testing.T) {
	a, b := NewStore(), NewStore()
	st := Sync(a, b, DefaultConfig)
	if !a.equal(b) {
		t.Fatal("empty stores should trivially converge")
	}
	if st.RecordsTransferred != 0 || st.RecordExchanges != 0 {
		t.Fatalf("no records should move for empty stores: %+v", st)
	}
	if st.DigestExchanges != 2 || st.NodesSkipped != 1 {
		t.Fatalf("only the root digest should be exchanged: %+v", st)
	}
	if st.Rounds != 1 {
		t.Fatalf("expected 1 round, got %d", st.Rounds)
	}
}

func TestSyncEmptyAgainstPopulated(t *testing.T) {
	a, b := NewStore(), NewStore()
	for i := 0; i < 50; i++ {
		a.Put(fmt.Sprintf("key-%d", i), []byte("v"), 1)
	}
	st := Sync(a, b, DefaultConfig)
	if !a.equal(b) {
		t.Fatal("stores did not converge")
	}
	if st.RecordsTransferred != 50 {
		t.Fatalf("expected 50 records transferred, got %d", st.RecordsTransferred)
	}
}

func TestSyncConvergesRandomDivergence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a, b := NewStore(), NewStore()
	// Shared base.
	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("shared-%d", i)
		a.Put(k, []byte("v1"), 1)
		b.Put(k, []byte("v1"), 1)
	}
	// Divergent updates on both sides.
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("shared-%d", rng.Intn(200))
		if rng.Intn(2) == 0 {
			a.Put(k, []byte("a-newer"), 2)
		} else {
			b.Put(k, []byte("b-newer"), 3)
		}
	}
	// Keys unique to each side, plus a delete on each side.
	for i := 0; i < 30; i++ {
		a.Put(fmt.Sprintf("only-a-%d", i), []byte("x"), 1)
		b.Put(fmt.Sprintf("only-b-%d", i), []byte("y"), 1)
	}
	a.Delete("shared-0", 5)
	b.Delete("shared-1", 5)

	st := Sync(a, b, DefaultConfig)
	if !a.equal(b) {
		t.Fatal("stores did not converge")
	}
	if r, _ := a.Get("shared-0"); !r.Deleted {
		t.Fatal("tombstone from a did not propagate")
	}
	if r, _ := b.Get("shared-0"); !r.Deleted {
		t.Fatal("tombstone not visible on b")
	}
	// Digests must agree everywhere after sync.
	ta, tb := buildDigestTree(a, DefaultConfig), buildDigestTree(b, DefaultConfig)
	if ta.root.digest != tb.root.digest {
		t.Fatal("root digests differ after sync")
	}
	// 200 shared keys must not have been re-shipped in bulk.
	if st.RecordsTransferred > 400 {
		t.Fatalf("digest pruning failed, transferred %d records", st.RecordsTransferred)
	}
	if st.NodesSkipped == 0 {
		t.Fatal("expected some subtrees to be skipped")
	}
	t.Logf("stats: %+v", st)
}

func TestTombstoneParticipatesInDigest(t *testing.T) {
	live, tomb, absent := NewStore(), NewStore(), NewStore()
	live.Put("k", []byte("v"), 1)
	tomb.Delete("k", 1)
	dLive := buildDigestTree(live, DefaultConfig).root.digest
	dTomb := buildDigestTree(tomb, DefaultConfig).root.digest
	dAbsent := buildDigestTree(absent, DefaultConfig).root.digest
	if dLive == dTomb {
		t.Fatal("tombstone must produce a different digest than a live record")
	}
	if dTomb == dAbsent {
		t.Fatal("tombstone must participate in the digest, not vanish")
	}
	// A delete on one side must override an older live record on the other.
	a, b := NewStore(), NewStore()
	a.Put("k", []byte("old"), 1)
	b.Put("k", []byte("old"), 1)
	a.Delete("k", 2)
	Sync(a, b, DefaultConfig)
	if r, _ := b.Get("k"); !r.Deleted || r.Version != 2 {
		t.Fatalf("delete did not propagate: %+v", r)
	}
}

func TestSkewedKeys(t *testing.T) {
	// Craft keys whose placement hashes crowd into a tiny fraction of the
	// key space, forcing deep unbalanced splits.
	var skewed []string
	for i := 0; len(skewed) < 64; i++ {
		k := fmt.Sprintf("skew-%d", i)
		if KeyHash(k) < 1<<56 {
			skewed = append(skewed, k)
		}
	}
	a, b := NewStore(), NewStore()
	for i, k := range skewed {
		a.Put(k, []byte(fmt.Sprintf("v%d", i)), 1)
		if i%3 != 0 { // b holds only a subset
			b.Put(k, []byte(fmt.Sprintf("v%d", i)), 1)
		}
	}
	st := Sync(a, b, DefaultConfig)
	if !a.equal(b) {
		t.Fatal("skewed stores did not converge")
	}
	if st.Rounds < 4 {
		t.Fatalf("skewed keys should force deep recursion, rounds=%d", st.Rounds)
	}
	ta, tb := buildDigestTree(a, DefaultConfig), buildDigestTree(b, DefaultConfig)
	assertSameShape(t, ta.root, tb.root)
}

func TestBuildOrderProducesSameTree(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	keys := make([]string, 300)
	for i := range keys {
		keys[i] = fmt.Sprintf("order-key-%d", i)
	}
	base := NewStore()
	for _, k := range keys {
		base.Put(k, []byte("v-"+k), 1)
	}
	base.Delete(keys[0], 2) // include a tombstone

	// Rebuild the same logical set in many different insertion orders.
	want := buildDigestTree(base, DefaultConfig)
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(keys))
		s := NewStore()
		for _, idx := range perm {
			k := keys[idx]
			if idx == 0 {
				s.Put(k, []byte("v-"+k), 1)
				s.Delete(k, 2)
			} else {
				s.Put(k, []byte("v-"+k), 1)
			}
		}
		got := buildDigestTree(s, DefaultConfig)
		assertSameShape(t, want.root, got.root)
		if want.root.digest != got.root.digest {
			t.Fatalf("trial %d: root digest differs across build orders", trial)
		}
	}
}

func TestSplitRuleDeterministicOnContents(t *testing.T) {
	// Same contents, built via different operation histories (including
	// overwritten puts and re-deletes), must yield identical trees.
	a := NewStore()
	for i := 0; i < 40; i++ {
		a.Put(fmt.Sprintf("k%d", i), []byte("x"), 1)
	}
	b := NewStore()
	for i := 39; i >= 0; i-- {
		b.Put(fmt.Sprintf("k%d", i), []byte("stale"), 0)
		b.Put(fmt.Sprintf("k%d", i), []byte("x"), 1)
	}
	ta, tb := buildDigestTree(a, DefaultConfig), buildDigestTree(b, DefaultConfig)
	assertSameShape(t, ta.root, tb.root)
	if ta.root.digest != tb.root.digest {
		t.Fatal("identical contents produced different digests")
	}
}

func TestStatsCountCommunication(t *testing.T) {
	a, b := NewStore(), NewStore()
	for i := 0; i < 16; i++ {
		k := fmt.Sprintf("c-%d", i)
		a.Put(k, []byte("v"), 1)
		b.Put(k, []byte("v"), 1)
	}
	a.Put("extra", []byte("e"), 1)
	st := Sync(a, b, DefaultConfig)
	if st.DigestExchanges == 0 || st.DigestExchanges%2 != 0 {
		t.Fatalf("digest exchanges must be positive and bidirectional: %+v", st)
	}
	if st.RecordExchanges == 0 || st.RecordsTransferred == 0 {
		t.Fatalf("leaf conflict must exchange real records: %+v", st)
	}
	if st.Rounds == 0 {
		t.Fatal("rounds must be counted")
	}
}

// assertSameShape recursively verifies two trees have identical structure
// and identical digests at every node.
func assertSameShape(t *testing.T, x, y *node) {
	t.Helper()
	if x.leaf != y.leaf {
		t.Fatalf("shape mismatch at depth %d range [%d,%d): leaf=%v vs %v",
			x.depth, x.lo, x.hi, x.leaf, y.leaf)
	}
	if x.digest != y.digest {
		t.Fatalf("digest mismatch at depth %d range [%d,%d)", x.depth, x.lo, x.hi)
	}
	if x.leaf {
		if len(x.keys) != len(y.keys) {
			t.Fatalf("leaf key count mismatch at depth %d", x.depth)
		}
		return
	}
	for i := 0; i < 2; i++ {
		assertSameShape(t, x.children[i], y.children[i])
	}
}
