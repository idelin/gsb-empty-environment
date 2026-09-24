package baselinepatch

import "testing"

func TestInverseMixedAddChangeDelete(t *testing.T) {
	base := map[string]any{
		"keep":   float64(1),
		"change": "a",
		"drop":   true,
		"lists": map[string]any{
			"l": []any{float64(1), float64(2), float64(3)},
		},
	}
	target := map[string]any{
		"keep":   float64(1),
		"change": "b",
		"born":   nil, // added as explicit null: inverse must delete it
		"lists": map[string]any{
			"l": []any{float64(1), float64(9), float64(2), float64(3)},
			"m": []any{float64(0)}, // brand-new array inside changed object
		},
	}
	p, err := Diff(base, target)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Apply(base, p)
	if err != nil {
		t.Fatal(err)
	}

	inv, err := p.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	// Inverse must serialize and validate like any patch.
	raw, err := inv.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	inv2, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Apply(out.(map[string]any), inv2)
	if err != nil {
		t.Fatalf("inverse apply: %v", err)
	}
	bd, _ := Digest(base)
	rd, _ := Digest(restored)
	if bd != rd {
		t.Fatal("inverse failed to restore baseline identity")
	}

	// The added null key inverts to a delete (not a set of the key to null).
	foundDelete := false
	for _, op := range inv.Ops {
		if op.Kind == OpDelete && len(op.Path) == 1 && op.Path[0] == "born" {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Fatal("inverse of an added null field must be a delete")
	}
}

func TestDoubleInverseIsEquivalent(t *testing.T) {
	base := doc()
	target := doc()
	target["tags"] = []any{"x", "w", "y", "z"}
	p, _ := Diff(base, target)
	inv, err := p.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	back, err := inv.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := p.Marshal()
	r2, _ := back.Marshal()
	if string(r1) != string(r2) {
		t.Fatal("inverse of inverse must reproduce the original patch")
	}
}
