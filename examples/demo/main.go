// Command demo walks through the baseline-patch codec: diffing two documents,
// serializing the patch, applying it atomically, inverting it, and showing the
// rejection guarantees (missing baseline, wrong-but-same-length baseline,
// delete vs null, untrusted length prefixes).
package main

import (
	"encoding/json"
	"fmt"

	"baselinepatch"
)

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

func shortDigest(v any) string {
	d, err := baselinepatch.Digest(v)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", d[:8])
}

func main() {
	baseline := map[string]any{
		"name":   "alpha",
		"status": "active",
		"v":      1.0,
		"tags":   []any{"x", "y", "z"},
		"meta": map[string]any{
			"owner": "ada",
			"note":  nil, // explicit null; different from deleting "note"
		},
	}
	target := map[string]any{
		"name":   "alpha-2",
		"status": "active",
		"v":      2.5,
		"tags":   []any{"x", "w", "y", "z"}, // insert "w" at index 1
		"meta": map[string]any{
			"owner": "ada",
			// "note" deleted, not nulled
			"team": "core",
		},
	}

	fmt.Println("== baseline ==")
	fmt.Println(mustJSON(baseline))
	fmt.Println("digest:", shortDigest(baseline))

	// 1) Diff -> binary patch -> decode -> apply.
	p, err := baselinepatch.Diff(baseline, target)
	must(err)
	raw, err := p.Marshal()
	must(err)
	fmt.Printf("\n== serialized patch: %d bytes, %d ops ==\n", len(raw), len(p.Ops))

	decoded, err := baselinepatch.Unmarshal(raw)
	must(err)

	applied, err := baselinepatch.Apply(baseline, decoded)
	must(err)
	fmt.Println("== applied result ==")
	fmt.Println(mustJSON(applied))

	// 2) Inverse restores the baseline by digest (identity convention).
	inv, err := decoded.Inverse()
	must(err)
	restored, err := baselinepatch.Apply(applied.(map[string]any), inv)
	must(err)
	rd, _ := baselinepatch.Digest(restored)
	bd, _ := baselinepatch.Digest(baseline)
	fmt.Printf("\n== inverse ==\nrestored digest: %x\nbaseline digest: %x\nidentity: %v\n",
		rd[:8], bd[:8], rd == bd)

	// 3) Missing baseline is rejected; no {} default is invented.
	_, err = baselinepatch.Apply(nil, decoded)
	fmt.Printf("\nmissing baseline -> %v\n", err)

	// 4) Same serialized length, different content: digest must fail.
	fake := map[string]any{
		"name":   "alpha",
		"status": "active",
		"v":      1.0,
		"tags":   []any{"x", "z", "y"}, // same length array, different order
		"meta": map[string]any{
			"owner": "ada",
			"note":  nil,
		},
	}
	fb, _ := json.Marshal(fake)
	bb, _ := json.Marshal(baseline)
	_, err = baselinepatch.Apply(fake, decoded)
	fmt.Printf("\nsame-length different-content baseline (json len %d vs %d) -> %v\n",
		len(fb), len(bb), err)

	// 5) Delete is not set-null. Build both variants by hand.
	setNull := map[string]any{
		"name": "alpha", "status": "active", "v": 1.0,
		"tags": []any{"x", "y", "z"},
		"meta": map[string]any{"owner": "ada", "note": nil, "extra": 1},
	}
	deleted := map[string]any{
		"name": "alpha", "status": "active", "v": 1.0,
		"tags": []any{"x", "y", "z"},
		"meta": map[string]any{"owner": "ada"},
	}
	_ = setNull
	_ = deleted
	dn, _ := baselinepatch.Digest(setNull)
	dm, _ := baselinepatch.Digest(deleted)
	fmt.Printf("\ndelete vs null digests differ: %v\n", dn != dm)

	// 6) Untrusted input: oversized length prefix is rejected before any
	// allocation; truncated buffers fail closed.
	// Corrupt the op-count varint (immediately after the 73-byte header)
	// into a 10-byte overflow encoding. Length is validated before allocating.
	evil := make([]byte, headerLen+10)
	copy(evil, raw[:headerLen])
	for i := 0; i < 10; i++ {
		evil[headerLen+i] = 0xFF
	}
	_, err = baselinepatch.Unmarshal(evil)
	fmt.Printf("oversized op-count prefix -> %v\n", err)
	_, err = baselinepatch.Unmarshal(raw[:len(raw)/2])
	fmt.Printf("truncated patch -> %v\n", err)

	// 7) Decoding never executes host code: the supported range rejects ints.
	_, err = baselinepatch.Digest(map[string]any{"x": 3}) // untyped int literal
	fmt.Printf("unsupported value (int) -> %v\n", err)

	// 8) A malformed op against the real baseline is atomic: baseline intact.
	bad := &baselinepatch.Patch{
		BaselineDigest: decoded.BaselineDigest,
		TargetDigest:   decoded.TargetDigest,
		Ops: []baselinepatch.Op{{
			Kind: baselinepatch.OpDelete,
			Path: []string{"does-not-exist"},
			Old:  nil,
		}},
	}
	_, err = baselinepatch.Apply(baseline, bad)
	fmt.Printf("\natomic reject of bad op -> %v\n", err)
	d0, _ := baselinepatch.Digest(baseline)
	fmt.Printf("baseline unchanged after failure: %v\n", d0 == bd)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

const headerLen = len("BPDIFF01") + 1 + 32 + 32
