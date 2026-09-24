package baselinepatch

import "testing"

func TestLargeArrayFallbackIdentity(t *testing.T) {
	// Arrays whose n*m exceeds maxLCSCells: Diff must still be exact and
	// invertible via a whole-array splice, without quadratic work.
	base := map[string]any{"big": make([]any, 5000)}
	target := map[string]any{"big": make([]any, 5000)}
	for i := range base["big"].([]any) {
		base["big"].([]any)[i] = float64(i)
		target["big"].([]any)[i] = float64(i + 1)
	}

	p, err := Diff(base, target)
	if err != nil {
		t.Fatal(err)
	}
	var splices int
	for _, op := range p.Ops {
		if op.Kind == OpSplice {
			splices++
		}
	}
	if splices != 1 {
		t.Fatalf("want 1 fallback splice, got %d", splices)
	}
	out, err := Apply(base, p)
	if err != nil {
		t.Fatal(err)
	}
	td, _ := Digest(target)
	od, _ := Digest(out)
	if td != od {
		t.Fatal("fallback splice produced wrong target")
	}
	inv, err := p.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Apply(out.(map[string]any), inv)
	if err != nil {
		t.Fatal(err)
	}
	bd, _ := Digest(base)
	rd, _ := Digest(restored)
	if bd != rd {
		t.Fatal("fallback splice inverse failed identity")
	}

	now, _ := Digest(base)
	if now != bd {
		t.Fatal("baseline mutated")
	}
}
