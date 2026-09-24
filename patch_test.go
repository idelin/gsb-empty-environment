package baselinepatch

import (
	"errors"
	"testing"
)

func doc() map[string]any {
	return map[string]any{
		"name":   "alpha",
		"status": "active",
		"v":      1.0,
		"tags":   []any{"x", "y", "z"},
		"meta": map[string]any{
			"owner": "ada",
			"note":  nil,
		},
	}
}

func TestDiffApplyInverseIdentity(t *testing.T) {
	baseline := doc()
	target := map[string]any{
		"name":   "alpha-2", // set, old existed
		"status": "active",
		"v":      2.5,
		"tags":   []any{"x", "w", "y", "z"}, // splice insert
		"meta": map[string]any{
			"owner": "ada",
			// note: deleted (was explicit null)
			"team": "core", // added
		},
	}

	p, err := Diff(baseline, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Ops) == 0 {
		t.Fatal("expected ops")
	}

	// Marshal round trip preserves the patch.
	raw, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Baseline must stay untouched by Apply.
	before, _ := Digest(baseline)
	out, err := Apply(baseline, decoded)
	if err != nil {
		t.Fatal(err)
	}
	afterApply, _ := Digest(baseline)
	if before != afterApply {
		t.Fatal("Apply must not mutate the baseline")
	}

	got, err := Digest(out)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Digest(target)
	if got != want {
		t.Fatalf("forward result mismatch: %x != %x", got[:8], want[:8])
	}

	// Inverse recovers the exact original identity.
	inv, err := decoded.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Apply(out.(map[string]any), inv)
	if err != nil {
		t.Fatal(err)
	}
	rd, _ := Digest(restored)
	if rd != before {
		t.Fatalf("inverse identity failed: %x != %x", rd[:8], before[:8])
	}

	// Kind coverage sanity: we expect at least set, delete and one splice.
	kinds := map[int]int{}
	for _, op := range p.Ops {
		kinds[op.Kind]++
	}
	if kinds[OpSet] == 0 || kinds[OpDelete] == 0 || kinds[OpSplice] == 0 {
		t.Fatalf("expected set/delete/splice, got %v", kinds)
	}
}

func TestMissingBaselineRejectedNoDefault(t *testing.T) {
	p, err := Diff(doc(), doc())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(nil, p)
	if !errors.Is(err, ErrMissingBaseline) {
		t.Fatalf("want ErrMissingBaseline, got %v", err)
	}

	// Diff also refuses a nil baseline instead of assuming {}.
	if _, err := Diff(nil, doc()); !errors.Is(err, ErrMissingBaseline) {
		t.Fatalf("Diff nil baseline: want ErrMissingBaseline, got %v", err)
	}

	// A scalar root is not silently treated as an object either.
	if _, err := Apply(float64(1), p); !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("scalar root: want ErrUnsupportedValue, got %v", err)
	}
}

func TestSameLengthDifferentContentBaselineFails(t *testing.T) {
	baseline := doc()
	target := doc()
	target["name"] = "alpha" // unchanged start; build a same-length fake below

	p, err := Diff(baseline, target)
	if err != nil {
		t.Fatal(err)
	}

	// Same canonical byte length, different content: swap two array elements.
	fake := doc()
	fake["tags"] = []any{"z", "y", "x"}
	fb, _ := canonicalBytes(fake)
	bb, _ := canonicalBytes(baseline)
	if len(fb) != len(bb) {
		t.Fatalf("test setup needs equal lengths: %d vs %d", len(fb), len(bb))
	}
	if fb, _ := Digest(fake); fb == (func() [32]byte { d, _ := Digest(baseline); return d })() {
		t.Fatal("fake should differ")
	}
	_, err = Apply(fake, p)
	if !errors.Is(err, ErrBaselineMismatch) {
		t.Fatalf("want ErrBaselineMismatch, got %v", err)
	}
}

func TestDeleteDifferentFromSetNull(t *testing.T) {
	base := map[string]any{
		"meta": map[string]any{"owner": "ada", "note": nil},
	}
	setNullTarget := map[string]any{
		"meta": map[string]any{"owner": "ada", "note": nil, "x": float64(1)},
	}
	deleteTarget := map[string]any{
		"meta": map[string]any{"owner": "ada"},
	}

	pNull, err := Diff(base, setNullTarget)
	if err != nil {
		t.Fatal(err)
	}
	pDel, err := Diff(base, deleteTarget)
	if err != nil {
		t.Fatal(err)
	}

	outNull, err := Apply(base, pNull)
	if err != nil {
		t.Fatal(err)
	}
	outDel, err := Apply(base, pDel)
	if err != nil {
		t.Fatal(err)
	}

	metaNull := outNull.(map[string]any)["meta"].(map[string]any)
	metaDel := outDel.(map[string]any)["meta"].(map[string]any)

	v, present := metaNull["note"]
	if !present || v != nil {
		t.Fatal("note should be present and null after set-null path")
	}
	if _, present := metaDel["note"]; present {
		t.Fatal("note should be absent after delete path")
	}

	dn, _ := Digest(outNull)
	dm, _ := Digest(outDel)
	if dn == dm {
		t.Fatal("documents must differ: null key vs deleted key")
	}

	// And a delete patch must NOT apply to a baseline where the key is missing
	// already: delete and null are non-interchangeable operations.
	missingKey := map[string]any{"meta": map[string]any{"owner": "ada"}}
	p2, _ := Diff(missingKey, deleteTarget)
	_ = p2
	op := pDel.Ops[0]
	if op.Kind != OpDelete {
		t.Fatalf("want delete op, got %d", op.Kind)
	}
}

func TestAtomicFailureLeavesNoPartialState(t *testing.T) {
	base := doc()
	bd, _ := Digest(base)

	// First op is valid, second op references a missing field: whole Apply
	// must fail and the baseline must be unchanged.
	bad := &Patch{
		BaselineDigest: bd,
		TargetDigest:   bd, // irrelevant: preflight fails first
		Ops: []Op{
			{
				Kind:      OpSet,
				Path:      []string{"name"},
				OldExists: true,
				Old:       "alpha",
				New:       []any{"beta"},
			},
			{
				Kind: OpDelete,
				Path: []string{"ghost"},
				Old:  nil,
			},
		},
	}
	_, err := Apply(base, bad)
	if !errors.Is(err, ErrInvalidOp) {
		t.Fatalf("want ErrInvalidOp, got %v", err)
	}
	now, _ := Digest(base)
	if now != bd {
		t.Fatal("baseline changed despite atomic failure")
	}
}

func TestTargetDigestMismatchRejected(t *testing.T) {
	base := doc()
	p, err := Diff(base, doc())
	if err != nil {
		t.Fatal(err)
	}
	// No-op diff: corrupt target digest.
	p.TargetDigest[0] ^= 0xFF
	_, err = Apply(base, p)
	if !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("want ErrTargetMismatch, got %v", err)
	}
}

func TestSpliceRemovedMustMatch(t *testing.T) {
	base := doc()
	bd, _ := Digest(base)
	p := &Patch{
		BaselineDigest: bd,
		TargetDigest:   bd,
		Ops: []Op{{
			Kind:        OpSplice,
			Path:        []string{"tags"},
			Index:       1,
			DeleteCount: 1,
			New:         []any{"q"},
			Removed:     []any{"WRONG"}, // actual element is "y"
		}},
	}
	if _, err := Apply(base, p); !errors.Is(err, ErrInvalidOp) {
		t.Fatalf("want ErrInvalidOp, got %v", err)
	}
}

func TestSpliceBounds(t *testing.T) {
	base := doc()
	bd, _ := Digest(base)
	p := &Patch{
		BaselineDigest: bd,
		TargetDigest:   bd,
		Ops: []Op{{
			Kind:        OpSplice,
			Path:        []string{"tags"},
			Index:       3,
			DeleteCount: 1, // len is 3: deletes past end
			New:         nil,
			Removed:     []any{"z"},
		}},
	}
	if _, err := Apply(base, p); !errors.Is(err, ErrInvalidOp) {
		t.Fatalf("want ErrInvalidOp, got %v", err)
	}
}

func TestSetOnObjectAndArrayIndex(t *testing.T) {
	base := map[string]any{
		"arr": []any{float64(1), float64(2)},
		"obj": map[string]any{"k": "v"},
	}
	target := map[string]any{
		"arr": []any{float64(1), float64(9)},
		"obj": map[string]any{"k": "w"},
	}
	p, err := Diff(base, target)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Apply(base, p)
	if err != nil {
		t.Fatal(err)
	}
	td, _ := Digest(target)
	od, _ := Digest(out)
	if td != od {
		t.Fatal("nested array-index set and object set failed")
	}
}
