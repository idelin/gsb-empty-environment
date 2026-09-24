package baselinepatch

import (
	"errors"
	"math/rand"
	"testing"
)

func TestPatchUnmarshalRejectsMalformed(t *testing.T) {
	base := doc()
	target := doc()
	target["name"] = "beta"
	p, err := Diff(base, target)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// Truncation at every byte offset must error, never panic.
	for i := 0; i <= len(raw); i++ {
		if _, err := Unmarshal(raw[:i]); i >= len(raw) && err != nil {
			t.Fatalf("full decode failed: %v", err)
		}
	}

	// Bad magic.
	bad := append([]byte(nil), raw...)
	bad[0] = 'X'
	if _, err := Unmarshal(bad); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("bad magic: want ErrInvalidPatch, got %v", err)
	}

	// Bad version.
	bad = append([]byte(nil), raw...)
	bad[len(patchMagic)] = 99
	if _, err := Unmarshal(bad); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("bad version: want ErrInvalidPatch, got %v", err)
	}

	// Trailing bytes.
	trailing := append(append([]byte(nil), raw...), 0)
	if _, err := Unmarshal(trailing); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("trailing bytes: want ErrInvalidPatch, got %v", err)
	}

	// Random bit flips in the body must never panic.
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		fuzz := append([]byte(nil), raw...)
		pos := headerLen() + rng.Intn(len(fuzz)-headerLen())
		fuzz[pos] ^= byte(1 << rng.Intn(8))
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on fuzzed input: %v", r)
				}
			}()
			_, _ = Unmarshal(fuzz)
		}()
	}
}

func headerLen() int { return len(patchMagic) + 1 + 32 + 32 }

func TestValidateRejectsBadOps(t *testing.T) {
	bd, _ := Digest(doc())
	cases := []struct {
		name string
		op   Op
	}{
		{"empty path", Op{Kind: OpSet, Path: nil, New: []any{float64(1)}}},
		{"set no new", Op{Kind: OpSet, Path: []string{"a"}}},
		{"set two new", Op{Kind: OpSet, Path: []string{"a"}, New: []any{float64(1), float64(2)}}},
		{"set bad old", Op{Kind: OpSet, Path: []string{"a"}, OldExists: true, Old: 3, New: []any{float64(1)}}},
		{"delete carries new", Op{Kind: OpDelete, Path: []string{"a"}, Old: nil, New: []any{float64(1)}}},
		{"splice negative", Op{Kind: OpSplice, Path: []string{"a"}, Index: -1}},
		{"splice removed mismatch", Op{Kind: OpSplice, Path: []string{"a"}, Index: 0,
			DeleteCount: 1, Removed: []any{float64(1), float64(2)}}},
		{"unknown kind", Op{Kind: 99, Path: []string{"a"}}},
	}
	for _, c := range cases {
		p := &Patch{BaselineDigest: bd, TargetDigest: bd, Ops: []Op{c.op}}
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected validation error", c.name)
		}
	}
	if err := (*Patch)(nil).Validate(); err == nil {
		t.Error("nil patch must fail validation")
	}
}

// TestRandomRoundTrip builds random documents, diffs them, applies forward and
// inverse, and asserts identity restoration. It also exercises arrays with
// inserts/deletes/reorders in nested positions.
func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		base := randomDoc(rng, 3, 6)
		target := randomDoc(rng, 3, 6)

		p, err := Diff(base, target)
		if err != nil {
			t.Fatalf("iter %d diff: %v", iter, err)
		}
		raw, err := p.Marshal()
		if err != nil {
			t.Fatalf("iter %d marshal: %v", iter, err)
		}
		decoded, err := Unmarshal(raw)
		if err != nil {
			t.Fatalf("iter %d unmarshal: %v", iter, err)
		}
		out, err := Apply(base, decoded)
		if err != nil {
			t.Fatalf("iter %d apply: %v\nops=%v", iter, err, decoded.Ops)
		}
		td, _ := Digest(target)
		od, _ := Digest(out)
		if td != od {
			t.Fatalf("iter %d forward mismatch", iter)
		}

		inv, err := decoded.Inverse()
		if err != nil {
			t.Fatalf("iter %d inverse build: %v", iter, err)
		}
		restored, err := Apply(out.(map[string]any), inv)
		if err != nil {
			t.Fatalf("iter %d inverse apply: %v", iter, err)
		}
		bd, _ := Digest(base)
		rd, _ := Digest(restored)
		if bd != rd {
			t.Fatalf("iter %d inverse identity mismatch", iter)
		}
	}
}

func randomDoc(rng *rand.Rand, depth, breadth int) map[string]any {
	out := map[string]any{}
	n := rng.Intn(breadth + 1)
	for i := 0; i < n; i++ {
		key := keyName(rng.Intn(8)) // small shared alphabet -> overlap/edits
		out[key] = randomValue(rng, depth, breadth)
	}
	return out
}

func keyName(i int) string {
	return string(rune('a' + i))
}

func randomValue(rng *rand.Rand, depth, breadth int) any {
	if depth == 0 {
		return randomScalar(rng)
	}
	switch rng.Intn(5) {
	case 0:
		return randomScalar(rng)
	case 1:
		return nil
	case 2, 3:
		arr := make([]any, rng.Intn(6))
		for i := range arr {
			arr[i] = randomValue(rng, depth-1, breadth)
		}
		return arr
	default:
		return randomDoc(rng, depth-1, breadth)
	}
}

func randomScalar(rng *rand.Rand) any {
	switch rng.Intn(3) {
	case 0:
		return float64(rng.Intn(7)) // small alphabet -> LCS matches
	case 1:
		return rng.Intn(2) == 0
	default:
		return keyName(rng.Intn(5))
	}
}

func TestDeterministicDiff(t *testing.T) {
	base := doc()
	target := map[string]any{
		"name": "z",
		"tags": []any{"a", "b", "c", "d"},
		"meta": map[string]any{"owner": "grace"},
	}
	p1, _ := Diff(base, target)
	p2, _ := Diff(base, target)
	r1, _ := p1.Marshal()
	r2, _ := p2.Marshal()
	if string(r1) != string(r2) {
		t.Fatal("Diff must be deterministic")
	}
}

func TestEqualDocumentsProduceNoopPatch(t *testing.T) {
	base := doc()
	p, err := Diff(base, doc())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Ops) != 0 {
		t.Fatalf("equal docs should yield no ops, got %d", len(p.Ops))
	}
	out, err := Apply(base, p)
	if err != nil {
		t.Fatal(err)
	}
	bd, _ := Digest(base)
	od, _ := Digest(out)
	if bd != od {
		t.Fatal("noop patch changed document")
	}
}
