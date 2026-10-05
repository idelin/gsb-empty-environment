package snapverify

import (
	"testing"
)

const testDepth = 4 // 16 ordered keys: 0..15

const testSize = uint64(1) << testDepth

func setupRef() *Reference {
	r := NewReference(testDepth)
	r.Set(1, []byte("alpha"))
	r.Set(4, []byte("beta"))
	r.Set(9, []byte("gamma"))
	r.Set(15, []byte("delta"))
	return r
}

func mustVerify(t *testing.T, oldRoot Hash, p *Proof, d *Delta) Hash {
	t.Helper()
	newRoot, err := Verify(testDepth, oldRoot, p, d)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	return newRoot
}

func mustBuildProof(t *testing.T, r *Reference, d *Delta) *Proof {
	t.Helper()
	p, err := r.BuildProof(d)
	if err != nil {
		t.Fatalf("BuildProof failed: %v", err)
	}
	return p
}

// TestEmptyDelta: an empty delta bumps the version but must leave the root
// untouched, and must not carry any proof nodes.
func TestEmptyDelta(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	d := &Delta{OldVersion: 7, NewVersion: 8, RangeLo: 0, RangeHi: testSize}
	p := mustBuildProof(t, r, d)
	if len(p.Nodes) != 0 {
		t.Fatalf("empty delta produced %d proof nodes", len(p.Nodes))
	}
	newRoot := mustVerify(t, oldRoot, p, d)
	if newRoot != oldRoot {
		t.Fatal("empty delta changed the root")
	}

	// An empty delta with smuggled proof nodes must be rejected.
	bad := &Proof{OldVersion: 7, NewVersion: 8, RangeLo: 0, RangeHi: testSize,
		Nodes: []ProofNode{{Level: 0, Index: 3, Hash: emptyLeafHash()}}}
	if _, err := Verify(testDepth, oldRoot, bad, d); err == nil {
		t.Fatal("empty delta with proof nodes accepted")
	}
}

// TestDuplicateKeyOperations: the same key appearing twice — within one list
// or across lists — must be rejected.
func TestDuplicateKeyOperations(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	cases := map[string]*Delta{
		"add twice": {
			OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
			Adds: []Entry{{Key: 3, Value: []byte("x")}, {Key: 3, Value: []byte("y")}},
		},
		"add and delete same key": {
			OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
			Adds:    []Entry{{Key: 4, Value: []byte("x")}},
			Deletes: []Entry{{Key: 4, Value: []byte("beta")}},
		},
		"delete and inherit same key": {
			OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
			Deletes:  []Entry{{Key: 9, Value: []byte("gamma")}},
			Inherits: []Entry{{Key: 9, Value: []byte("gamma")}},
		},
		"delete twice": {
			OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
			Deletes: []Entry{{Key: 1, Value: []byte("alpha")}, {Key: 1, Value: []byte("alpha")}},
		},
	}
	for name, d := range cases {
		p := &Proof{OldVersion: d.OldVersion, NewVersion: d.NewVersion, RangeLo: d.RangeLo, RangeHi: d.RangeHi}
		if _, err := Verify(testDepth, oldRoot, p, d); err == nil {
			t.Errorf("%s: duplicate key operation accepted", name)
		}
	}
}

// TestProofNodeOrder: verification keys proof nodes by position, so any
// order verifies; but missing, duplicated, or extra nodes are rejected.
func TestProofNodeOrder(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	d := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Adds:     []Entry{{Key: 2, Value: []byte("new-two")}, {Key: 13, Value: []byte("new-thirteen")}},
		Deletes:  []Entry{{Key: 4, Value: []byte("beta")}},
		Inherits: []Entry{{Key: 9, Value: []byte("gamma")}},
	}
	p := mustBuildProof(t, r, d)
	if err := r.ApplyDelta(d); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	want := r.Root()

	// Reversed order must verify identically.
	rev := *p
	rev.Nodes = make([]ProofNode, len(p.Nodes))
	for i, n := range p.Nodes {
		rev.Nodes[len(p.Nodes)-1-i] = n
	}
	if got := mustVerify(t, oldRoot, &rev, d); got != want {
		t.Fatal("reversed proof node order changed the result")
	}

	// Missing node.
	missing := *p
	missing.Nodes = p.Nodes[:len(p.Nodes)-1]
	if _, err := Verify(testDepth, oldRoot, &missing, d); err == nil {
		t.Fatal("proof with a missing node accepted")
	}

	// Duplicated node.
	dup := *p
	dup.Nodes = append(append([]ProofNode{}, p.Nodes...), p.Nodes[0])
	if _, err := Verify(testDepth, oldRoot, &dup, d); err == nil {
		t.Fatal("proof with a duplicated node accepted")
	}

	// Extra (foreign) node.
	extra := *p
	extra.Nodes = append(append([]ProofNode{}, p.Nodes...), ProofNode{Level: 0, Index: 7, Hash: emptyLeafHash()})
	if _, err := Verify(testDepth, oldRoot, &extra, d); err == nil {
		t.Fatal("proof with an extra node accepted")
	}
}

// TestCrossVersionChaining: a chain of deltas v0->v1->v2->v3 where each
// verified new root becomes the trusted old root of the next step; the
// final root must equal the full-rebuild reference. Replaying a stale
// delta against the advanced chain must fail.
func TestCrossVersionChaining(t *testing.T) {
	r := setupRef()
	root := r.Root()
	deltas := []*Delta{
		{
			OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
			Adds:    []Entry{{Key: 2, Value: []byte("v1-add")}},
			Deletes: []Entry{{Key: 1, Value: []byte("alpha")}},
		},
		{
			OldVersion: 1, NewVersion: 2, RangeLo: 0, RangeHi: testSize,
			Adds:     []Entry{{Key: 7, Value: []byte("v2-add")}},
			Inherits: []Entry{{Key: 4, Value: []byte("beta")}},
		},
		{
			OldVersion: 2, NewVersion: 3, RangeLo: 0, RangeHi: testSize,
			Deletes: []Entry{{Key: 15, Value: []byte("delta")}},
			Adds:    []Entry{{Key: 0, Value: []byte("v3-add")}},
		},
	}
	for i, d := range deltas {
		p := mustBuildProof(t, r, d)
		newRoot := mustVerify(t, root, p, d)
		if err := r.ApplyDelta(d); err != nil {
			t.Fatalf("step %d: ApplyDelta: %v", i, err)
		}
		if newRoot != r.Root() {
			t.Fatalf("step %d: incremental root != full-rebuild root", i)
		}
		root = newRoot
	}

	// Replaying the first delta (stale version) against the current root
	// must fail: the reconstructed old root no longer matches.
	staleProof := &Proof{OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize}
	if _, err := Verify(testDepth, root, staleProof, deltas[0]); err == nil {
		t.Fatal("stale delta replayed against advanced chain")
	}

	// A delta that skips a version must be rejected outright.
	skipped := &Delta{OldVersion: 3, NewVersion: 5, RangeLo: 0, RangeHi: testSize,
		Adds: []Entry{{Key: 3, Value: []byte("x")}}}
	skippedProof := &Proof{OldVersion: 3, NewVersion: 5, RangeLo: 0, RangeHi: testSize}
	if _, err := Verify(testDepth, root, skippedProof, skipped); err == nil {
		t.Fatal("version skip accepted")
	}
}

// TestOmittedDelete: if the prover drops a declared delete but keeps the
// honest new root, verification must fail.
func TestOmittedDelete(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	honest := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Adds:    []Entry{{Key: 2, Value: []byte("two")}},
		Deletes: []Entry{{Key: 4, Value: []byte("beta")}},
	}
	p := mustBuildProof(t, r, honest)
	honestRoot := mustVerify(t, oldRoot, p, honest)

	// Attacker silently keeps key 4: removes the delete from the delta.
	// The required proof-node set changes, so the honest proof no longer
	// fits, and even a rebuilt proof cannot reproduce honestRoot.
	cheating := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Adds: []Entry{{Key: 2, Value: []byte("two")}},
	}
	if _, err := Verify(testDepth, oldRoot, p, cheating); err == nil {
		t.Fatal("delta with omitted delete verified against honest proof")
	}
	cheatRef := setupRef()
	cheatProof := mustBuildProof(t, cheatRef, cheating)
	got := mustVerify(t, oldRoot, cheatProof, cheating)
	if got == honestRoot {
		t.Fatal("omitted delete produced the honest new root")
	}
}

// TestUndeclaredBlockSwap: any change to a key not present in the delta is
// pinned by proof hashes and must alter the resulting root.
func TestUndeclaredBlockSwap(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	d := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Adds: []Entry{{Key: 2, Value: []byte("two")}},
	}
	p := mustBuildProof(t, r, d)
	newRoot := mustVerify(t, oldRoot, p, d)

	// Tampered snapshot: apply the delta but also swap undeclared key 9.
	tampered := setupRef()
	if err := tampered.ApplyDelta(d); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	tampered.Set(9, []byte("evil"))
	if tampered.Root() == newRoot {
		t.Fatal("undeclared block swap did not change the root")
	}

	// Lying about an inherited value must break the old-root check.
	liar := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Adds:     []Entry{{Key: 2, Value: []byte("two")}},
		Inherits: []Entry{{Key: 9, Value: []byte("evil")}},
	}
	if _, err := Verify(testDepth, oldRoot, p, liar); err == nil {
		t.Fatal("forged inherit value accepted")
	}
}

// TestHeaderBinding: the proof must be bound to the same versions and range
// as the delta, and delta keys must stay inside the declared range.
func TestHeaderBinding(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	d := &Delta{
		OldVersion: 10, NewVersion: 11, RangeLo: 2, RangeHi: 12,
		Adds: []Entry{{Key: 5, Value: []byte("five")}},
	}
	p := mustBuildProof(t, r, d)
	if got := mustVerify(t, oldRoot, p, d); got == (Hash{}) {
		t.Fatal("unexpected zero root")
	}

	// Mismatched proof headers.
	for name, mutate := range map[string]func(*Proof){
		"old version": func(q *Proof) { q.OldVersion = 9 },
		"new version": func(q *Proof) { q.NewVersion = 12 },
		"range lo":    func(q *Proof) { q.RangeLo = 0 },
		"range hi":    func(q *Proof) { q.RangeHi = testSize },
	} {
		bad := *p
		bad.Nodes = p.Nodes
		mutate(&bad)
		if _, err := Verify(testDepth, oldRoot, &bad, d); err == nil {
			t.Errorf("proof with mismatched %s accepted", name)
		}
	}

	// Delta key outside its declared range.
	outOfRange := &Delta{
		OldVersion: 10, NewVersion: 11, RangeLo: 2, RangeHi: 12,
		Adds: []Entry{{Key: 14, Value: []byte("x")}},
	}
	if _, err := Verify(testDepth, oldRoot, p, outOfRange); err == nil {
		t.Fatal("key outside declared range accepted")
	}
}

// TestWrongOldRoot: verification against a root the receiver does not
// actually hold must fail.
func TestWrongOldRoot(t *testing.T) {
	r := setupRef()
	d := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Deletes: []Entry{{Key: 1, Value: []byte("alpha")}},
	}
	p := mustBuildProof(t, r, d)
	var garbage Hash
	garbage[0] = 0xff
	if _, err := Verify(testDepth, garbage, p, d); err != ErrOldRootMismatch {
		t.Fatalf("want ErrOldRootMismatch, got %v", err)
	}
}

// TestAddDeleteInheritAgainstReference: a mixed delta verified
// incrementally must match the full-rebuild reference exactly.
func TestAddDeleteInheritAgainstReference(t *testing.T) {
	r := setupRef()
	oldRoot := r.Root()
	d := &Delta{
		OldVersion: 0, NewVersion: 1, RangeLo: 0, RangeHi: testSize,
		Adds:     []Entry{{Key: 0, Value: []byte("zero")}, {Key: 8, Value: []byte("eight")}, {Key: 14, Value: []byte("fourteen")}},
		Deletes:  []Entry{{Key: 1, Value: []byte("alpha")}, {Key: 15, Value: []byte("delta")}},
		Inherits: []Entry{{Key: 4, Value: []byte("beta")}, {Key: 9, Value: []byte("gamma")}},
	}
	p := mustBuildProof(t, r, d)
	newRoot := mustVerify(t, oldRoot, p, d)
	if err := r.ApplyDelta(d); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	if newRoot != r.Root() {
		t.Fatal("incremental verification diverged from full rebuild")
	}

	// Adding over an occupied key must fail the old-root check.
	badAdd := &Delta{
		OldVersion: 1, NewVersion: 2, RangeLo: 0, RangeHi: testSize,
		Adds: []Entry{{Key: 4, Value: []byte("overwrite")}},
	}
	badProof := mustBuildProof(t, r, badAdd)
	if _, err := Verify(testDepth, newRoot, badProof, badAdd); err != ErrOldRootMismatch {
		t.Fatalf("add over occupied key: want ErrOldRootMismatch, got %v", err)
	}
}
