package snapdelta

import (
	"math/rand"
	"testing"
)

func applyOpsRef(m map[uint16][]byte, ops []Op) map[uint16][]byte {
	out := make(map[uint16][]byte, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, op := range ops {
		if op.Type == OpPut {
			out[op.Key] = op.Value
		} else {
			delete(out, op.Key)
		}
	}
	return out
}

func randomMap(r *rand.Rand, n int) map[uint16][]byte {
	m := make(map[uint16][]byte, n)
	for len(m) < n {
		k := uint16(r.Intn(KeySpaceSize))
		v := make([]byte, 1+r.Intn(8))
		r.Read(v)
		m[k] = v
	}
	return m
}

func randomOps(r *rand.Rand, n int) []Op {
	ops := make([]Op, 0, n)
	for i := 0; i < n; i++ {
		k := uint16(r.Intn(KeySpaceSize))
		if r.Intn(3) == 0 {
			ops = append(ops, Op{Type: OpDelete, Key: k})
		} else {
			v := make([]byte, 1+r.Intn(8))
			r.Read(v)
			ops = append(ops, Op{Type: OpPut, Key: k, Value: v})
		}
	}
	return ops
}

func mustVerify(t *testing.T, oldRoot Hash, oldVersion uint64, d *Delta, p *Proof) (Hash, uint64) {
	t.Helper()
	newRoot, newVersion, err := Verify(oldRoot, oldVersion, d, p)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	return newRoot, newVersion
}

// Empty delta on a non-empty map: root unchanged, version bumped.
func TestEmptyDelta(t *testing.T) {
	m := map[uint16][]byte{1: []byte("a"), 2: []byte("b"), 65535: []byte("z")}
	d, p, newMap, err := BuildDelta(m, 7, nil, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 1 || p.Nodes[0].Depth != 0 || p.Nodes[0].Index != 0 {
		t.Fatalf("empty delta should pin the whole tree with one root node, got %+v", p.Nodes)
	}
	newRoot, newVersion := mustVerify(t, FullRoot(m), 7, d, p)
	if newRoot != FullRoot(m) {
		t.Fatal("empty delta changed the root")
	}
	if newVersion != 8 {
		t.Fatalf("version = %d, want 8", newVersion)
	}
	if FullRoot(newMap) != FullRoot(m) {
		t.Fatal("builder map mismatch")
	}
}

// Empty delta on an empty map needs no proof nodes at all.
func TestEmptyDeltaEmptyMap(t *testing.T) {
	d, p, _, err := BuildDelta(map[uint16][]byte{}, 1, nil, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 0 {
		t.Fatalf("expected no proof nodes, got %d", len(p.Nodes))
	}
	newRoot, _ := mustVerify(t, DefaultRoot(), 1, d, p)
	if newRoot != DefaultRoot() {
		t.Fatal("root of empty snapshot must stay the default root")
	}
}

// Duplicate keys in the op list: applied in order, last one wins.
func TestDuplicateKeyOps(t *testing.T) {
	m := map[uint16][]byte{5: []byte("old"), 9: []byte("keep")}
	ops := []Op{
		{Type: OpPut, Key: 5, Value: []byte("v1")},
		{Type: OpPut, Key: 5, Value: []byte("v2")},
		{Type: OpDelete, Key: 5},
		{Type: OpPut, Key: 5, Value: []byte("v3")},
		{Type: OpDelete, Key: 9},
		{Type: OpDelete, Key: 9}, // deleting twice is a no-op
	}
	d, p, newMap, err := BuildDelta(m, 1, ops, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if string(newMap[5]) != "v3" {
		t.Fatalf("last write should win, got %q", newMap[5])
	}
	if _, ok := newMap[9]; ok {
		t.Fatal("key 9 should be deleted")
	}
	newRoot, _ := mustVerify(t, FullRoot(m), 1, d, p)
	if newRoot != FullRoot(applyOpsRef(m, ops)) {
		t.Fatal("verified root != full rebuild")
	}
	if len(p.OldEntries) != 2 { // keys 5 and 9 both existed
		t.Fatalf("old entries = %d, want 2", len(p.OldEntries))
	}
}

// Proof nodes must be in canonical (depth, index) order with no duplicates.
func TestProofNodeOrder(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	m := randomMap(r, 64)
	ops := randomOps(r, 8)
	d, p, _, err := BuildDelta(m, 1, ops, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) < 2 {
		t.Skip("need at least 2 proof nodes for order tests")
	}
	oldRoot := FullRoot(m)

	// Reversed node order must be rejected.
	rev := *p
	rev.Nodes = make([]ProofNode, len(p.Nodes))
	for i, n := range p.Nodes {
		rev.Nodes[len(p.Nodes)-1-i] = n
	}
	if _, _, err := Verify(oldRoot, 1, d, &rev); err == nil {
		t.Fatal("shuffled proof nodes accepted")
	}

	// Duplicated node must be rejected.
	dup := *p
	dup.Nodes = append(append([]ProofNode{}, p.Nodes...), p.Nodes[0])
	if _, _, err := Verify(oldRoot, 1, d, &dup); err == nil {
		t.Fatal("duplicated proof node accepted")
	}

	// Shuffled old entries must be rejected too.
	if len(p.OldEntries) >= 2 {
		bad := *p
		bad.OldEntries = append([]Entry{}, p.OldEntries...)
		bad.OldEntries[0], bad.OldEntries[1] = bad.OldEntries[1], bad.OldEntries[0]
		if _, _, err := Verify(oldRoot, 1, d, &bad); err == nil {
			t.Fatal("unsorted old entries accepted")
		}
	}

	// Sanity: the untouched proof verifies.
	mustVerify(t, oldRoot, 1, d, p)
}

// Cross-version chaining: the new root/version of one delta is the old
// root/version of the next.
func TestCrossVersionChaining(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	m0 := randomMap(r, 40)

	d1, p1, m1, err := BuildDelta(m0, 1, randomOps(r, 10), 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	root1, v1 := mustVerify(t, FullRoot(m0), 1, d1, p1)
	if root1 != FullRoot(m1) || v1 != 2 {
		t.Fatal("first hop mismatch")
	}

	d2, p2, m2, err := BuildDelta(m1, v1, randomOps(r, 10), 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	root2, v2 := mustVerify(t, root1, v1, d2, p2)
	if root2 != FullRoot(m2) || v2 != 3 {
		t.Fatal("second hop mismatch")
	}

	// Reusing the first hop's version/root for the second delta fails.
	if _, _, err := Verify(root1, 1, d2, p2); err == nil {
		t.Fatal("stale version accepted")
	}
	if _, _, err := Verify(FullRoot(m0), v1, d2, p2); err == nil {
		t.Fatal("stale root accepted")
	}
}

// A deletion dropped from the declared ops must not verify.
func TestOmittedDeletion(t *testing.T) {
	m := map[uint16][]byte{10: []byte("x"), 20: []byte("y"), 30: []byte("z")}
	ops := []Op{{Type: OpDelete, Key: 20}}
	d, p, _, err := BuildDelta(m, 1, ops, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := FullRoot(m)
	mustVerify(t, oldRoot, 1, d, p)

	// Drop the delete but keep the claimed new root.
	dropped := &Delta{NewRoot: d.NewRoot}
	if _, _, err := Verify(oldRoot, 1, dropped, p); err == nil {
		t.Fatal("omitted deletion accepted")
	}

	// Replace the delete with a no-op put of the old value: the claimed
	// new root must not match either.
	noop := &Delta{NewRoot: d.NewRoot, Ops: []Op{{Type: OpPut, Key: 20, Value: []byte("y")}}}
	if _, _, err := Verify(oldRoot, 1, noop, p); err == nil {
		t.Fatal("deletion replaced by no-op put accepted")
	}
}

// Inherited blocks cannot be swapped or tampered with.
func TestSwappedUndeclaredBlock(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	m := randomMap(r, 64)
	ops := randomOps(r, 6)
	d, p, _, err := BuildDelta(m, 1, ops, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) < 2 {
		t.Skip("need at least 2 proof nodes")
	}
	oldRoot := FullRoot(m)

	// Flip one bit in an inherited block hash.
	tampered := *p
	tampered.Nodes = append([]ProofNode{}, p.Nodes...)
	tampered.Nodes[0].Hash[0] ^= 0xff
	if _, _, err := Verify(oldRoot, 1, d, &tampered); err == nil {
		t.Fatal("tampered inherited block accepted")
	}

	// Swap the hashes of two inherited blocks (positions unchanged).
	swapped := *p
	swapped.Nodes = append([]ProofNode{}, p.Nodes...)
	swapped.Nodes[0].Hash, swapped.Nodes[1].Hash = swapped.Nodes[1].Hash, swapped.Nodes[0].Hash
	if _, _, err := Verify(oldRoot, 1, d, &swapped); err == nil {
		t.Fatal("swapped inherited blocks accepted")
	}

	// Extra junk node must be rejected.
	extra := *p
	extra.Nodes = append(append([]ProofNode{}, p.Nodes...), ProofNode{Depth: Depth, Index: 65535, Hash: Hash{1, 2, 3}})
	if _, _, err := Verify(oldRoot, 1, d, &extra); err == nil {
		t.Fatal("extra proof node accepted")
	}
}

// Old entries bind the pre-delta state of touched keys.
func TestOldEntryTampering(t *testing.T) {
	m := map[uint16][]byte{100: []byte("a"), 200: []byte("b")}
	ops := []Op{{Type: OpPut, Key: 100, Value: []byte("c")}}
	d, p, _, err := BuildDelta(m, 1, ops, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := FullRoot(m)

	bad := *p
	bad.OldEntries = append([]Entry{}, p.OldEntries...)
	bad.OldEntries[0].Value = []byte("forged")
	if _, _, err := Verify(oldRoot, 1, d, &bad); err == nil {
		t.Fatal("forged old entry accepted")
	}

	extra := *p
	extra.OldEntries = append(append([]Entry{}, p.OldEntries...), Entry{Key: 200, Value: []byte("b")})
	if _, _, err := Verify(oldRoot, 1, d, &extra); err == nil {
		t.Fatal("old entry for untouched key accepted")
	}
}

// Ops and old entries must stay inside the proven range.
func TestRangeBinding(t *testing.T) {
	m := map[uint16][]byte{10: []byte("a"), 5000: []byte("b")}
	ops := []Op{{Type: OpPut, Key: 10, Value: []byte("c")}}

	if _, _, _, err := BuildDelta(m, 1, []Op{{Type: OpPut, Key: 5000, Value: []byte("x")}}, 0, 100); err == nil {
		t.Fatal("builder accepted op outside range")
	}

	d, p, _, err := BuildDelta(m, 1, ops, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustVerify(t, FullRoot(m), 1, d, p)

	// Smuggle in an op outside the proven range.
	d2 := &Delta{NewRoot: d.NewRoot, Ops: append(append([]Op{}, d.Ops...), Op{Type: OpDelete, Key: 5000})}
	if _, _, err := Verify(FullRoot(m), 1, d2, p); err == nil {
		t.Fatal("op outside proven range accepted")
	}
}

// Version binding: the proof must chain from the receiver's version.
func TestVersionBinding(t *testing.T) {
	m := map[uint16][]byte{1: []byte("a")}
	d, p, _, err := BuildDelta(m, 5, []Op{{Type: OpPut, Key: 2, Value: []byte("b")}}, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Verify(FullRoot(m), 4, d, p); err == nil {
		t.Fatal("wrong old version accepted")
	}
	bad := *p
	bad.NewVersion = bad.OldVersion
	if _, _, err := Verify(FullRoot(m), 5, d, &bad); err == nil {
		t.Fatal("non-increasing version accepted")
	}
	if _, v, err := Verify(FullRoot(m), 5, d, p); err != nil || v != 6 {
		t.Fatalf("valid proof rejected: %v", err)
	}
}

// Randomized round trip: verified root always equals a full rebuild.
func TestRandomRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 30; iter++ {
		m := randomMap(r, 1+r.Intn(60))
		ops := randomOps(r, r.Intn(20))
		d, p, newMap, err := BuildDelta(m, uint64(iter), ops, 0, 65535)
		if err != nil {
			t.Fatal(err)
		}
		newRoot, newVersion := mustVerify(t, FullRoot(m), uint64(iter), d, p)
		if want := FullRoot(newMap); newRoot != want {
			t.Fatalf("iter %d: verified root != full rebuild", iter)
		}
		if newVersion != uint64(iter)+1 {
			t.Fatalf("iter %d: bad version", iter)
		}
		if FullRoot(applyOpsRef(m, ops)) != newRoot {
			t.Fatalf("iter %d: reference application mismatch", iter)
		}
	}
}

// Values are committed, not just keys: a put with a different value than
// the one used to compute the claimed root must fail.
func TestValueBinding(t *testing.T) {
	m := map[uint16][]byte{7: []byte("a")}
	d, p, _, err := BuildDelta(m, 1, []Op{{Type: OpPut, Key: 7, Value: []byte("real")}}, 0, 65535)
	if err != nil {
		t.Fatal(err)
	}
	forged := &Delta{NewRoot: d.NewRoot, Ops: []Op{{Type: OpPut, Key: 7, Value: []byte("fake")}}}
	if _, _, err := Verify(FullRoot(m), 1, forged, p); err == nil {
		t.Fatal("forged value accepted")
	}
}
