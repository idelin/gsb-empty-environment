package gsbdiff

import (
	"errors"
	"reflect"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	n, err := Normalize(v)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	b, err := canonical(n)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return string(b)
}

func baseline() map[string]any {
	return map[string]any{
		"name": "alice",
		"tags": []any{"a", "b", "c"},
		"meta": map[string]any{"age": float64(30), "city": "sh"},
	}
}

func target() map[string]any {
	return map[string]any{
		"name": "alice",
		"tags": []any{"a", "x", "y", "c"},
		"meta": map[string]any{"age": float64(31)},
		"note": nil,
	}
}

func TestRoundTripThroughWire(t *testing.T) {
	d, err := Compute(baseline(), target())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	wire, err := Encode(d)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d2, err := Decode(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, _, err := Apply(baseline(), d2)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got, want := mustJSON(t, got), mustJSON(t, target()); got != want {
		t.Fatalf("round trip mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestInverseRestoresIdentity(t *testing.T) {
	base := baseline()
	d, err := Compute(base, target())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	fwd, inv, err := Apply(base, d)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	restored, _, err := Apply(fwd, inv)
	if err != nil {
		t.Fatalf("apply inverse: %v", err)
	}
	if got, want := mustJSON(t, restored), mustJSON(t, base); got != want {
		t.Fatalf("inverse did not restore identity:\n got %s\nwant %s", got, want)
	}
}

func TestMissingBaselineRejected(t *testing.T) {
	d, err := Compute(baseline(), target())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if _, _, err := Apply(nil, d); !errors.Is(err, ErrBaselineRequired) {
		t.Fatalf("want ErrBaselineRequired, got %v", err)
	}
}

func TestSameLengthDifferentContentBaselineRejected(t *testing.T) {
	d, err := Compute(map[string]any{"a": float64(1)}, map[string]any{"a": float64(2)})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	// {"a":1} and {"b":1} have identical canonical length, different content.
	other := map[string]any{"b": float64(1)}
	if mustJSON(t, other) == mustJSON(t, map[string]any{"a": float64(1)}) {
		t.Fatal("test setup: baselines should differ")
	}
	if len(mustJSON(t, other)) != len(mustJSON(t, map[string]any{"a": float64(1)})) {
		t.Fatal("test setup: baselines should have equal canonical length")
	}
	if _, _, err := Apply(other, d); !errors.Is(err, ErrBaselineMismatch) {
		t.Fatalf("want ErrBaselineMismatch, got %v", err)
	}
}

func TestDeleteDiffersFromSetNull(t *testing.T) {
	base := map[string]any{"k": float64(1), "keep": true}

	delDiff, err := Compute(base, map[string]any{"keep": true})
	if err != nil {
		t.Fatalf("compute del: %v", err)
	}
	delRes, inv, err := Apply(base, delDiff)
	if err != nil {
		t.Fatalf("apply del: %v", err)
	}
	if _, ok := delRes.(map[string]any)["k"]; ok {
		t.Fatal("delete: key k should be absent")
	}
	restored, _, err := Apply(delRes, inv)
	if err != nil {
		t.Fatalf("apply inverse of delete: %v", err)
	}
	if got, want := mustJSON(t, restored), mustJSON(t, base); got != want {
		t.Fatalf("inverse of delete mismatch: got %s want %s", got, want)
	}

	nullDiff, err := Compute(base, map[string]any{"k": nil, "keep": true})
	if err != nil {
		t.Fatalf("compute null: %v", err)
	}
	nullRes, _, err := Apply(base, nullDiff)
	if err != nil {
		t.Fatalf("apply null: %v", err)
	}
	v, ok := nullRes.(map[string]any)["k"]
	if !ok || v != nil {
		t.Fatalf("set null: key k should be present and null, got ok=%v v=%v", ok, v)
	}
	if mustJSON(t, delRes) == mustJSON(t, nullRes) {
		t.Fatal("delete and set-null must produce different documents")
	}
}

func TestAtomicApplyLeavesBaselineUntouched(t *testing.T) {
	base := baseline()
	before := mustJSON(t, base)
	// Hand-built diff whose array edit overruns the array.
	d := &Diff{}
	canon, _ := canonical(mustNorm(t, base))
	d.BaseLen, d.BaseHash = summaryOf(canon)
	d.Ops = []Op{{
		Kind: OpArrayEdit,
		Path: []Seg{{Key: "tags"}},
		Edits: []Edit{
			{Kind: EditKeep, N: 2},
			{Kind: EditDelete, N: 5}, // overruns: only 1 element left
		},
	}}
	if _, _, err := Apply(base, d); !errors.Is(err, ErrInvalidOp) {
		t.Fatalf("want ErrInvalidOp, got %v", err)
	}
	if after := mustJSON(t, base); after != before {
		t.Fatalf("baseline mutated by failed apply:\nbefore %s\nafter  %s", before, after)
	}
}

func TestArrayEditScript(t *testing.T) {
	base := map[string]any{"list": []any{float64(1), float64(2), float64(3), float64(4)}}
	want := map[string]any{"list": []any{float64(1), float64(9), float64(8), float64(4)}}
	d, err := Compute(base, want)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	got, inv, err := Apply(base, d)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(mustNorm(t, got), mustNorm(t, want)) {
		t.Fatalf("array edit mismatch: got %v want %v", got, want)
	}
	restored, _, err := Apply(got, inv)
	if err != nil {
		t.Fatalf("inverse: %v", err)
	}
	if !reflect.DeepEqual(mustNorm(t, restored), mustNorm(t, base)) {
		t.Fatalf("array inverse mismatch: got %v want %v", restored, base)
	}
}

func TestUnsupportedValuesRejected(t *testing.T) {
	if _, err := Compute(map[string]any{"f": func() {}}, map[string]any{}); !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("func value: want ErrUnsupportedValue, got %v", err)
	}
	if _, err := Compute(map[string]any{"x": 1.0}, map[string]any{"x": nan()}); !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("NaN value: want ErrUnsupportedValue, got %v", err)
	}
}

func TestScalarRootRejected(t *testing.T) {
	if _, err := Compute("just a string", map[string]any{}); !errors.Is(err, ErrNotDocument) {
		t.Fatalf("want ErrNotDocument, got %v", err)
	}
}

func mustNorm(t *testing.T, v any) any {
	t.Helper()
	n, err := Normalize(v)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return n
}

func nan() float64 {
	z := 0.0
	return z / z
}
