// Demo: baseline-summary diff with atomic apply and inverse diff.
package main

import (
	"encoding/json"
	"fmt"

	"gsbdiff"
)

func pretty(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func main() {
	base := map[string]any{
		"name": "alice",
		"tags": []any{"a", "b", "c"},
		"meta": map[string]any{"age": 30, "city": "sh"},
	}
	next := map[string]any{
		"name": "alice",
		"tags": []any{"a", "x", "y", "c"},
		"meta": map[string]any{"age": 31}, // city deleted
		"note": nil,                       // explicit null, distinct from delete
	}

	fmt.Println("== baseline ==")
	fmt.Println(pretty(base))
	fmt.Println("== target ==")
	fmt.Println(pretty(next))

	d, err := gsbdiff.Compute(base, next)
	must("compute", err)
	wire, err := gsbdiff.Encode(d)
	must("encode", err)
	fmt.Printf("== diff: %d ops, %d wire bytes ==\n", len(d.Ops), len(wire))

	d2, err := gsbdiff.Decode(wire)
	must("decode", err)
	got, inv, err := gsbdiff.Apply(base, d2)
	must("apply", err)
	fmt.Println("== applied ==")
	fmt.Println(pretty(got))

	restored, _, err := gsbdiff.Apply(got, inv)
	must("apply inverse", err)
	fmt.Println("== forward+inverse restores original (identity) ==")
	fmt.Println(pretty(restored), "equal:", pretty(restored) == pretty(base))

	fmt.Println("== rejections ==")
	_, _, err = gsbdiff.Apply(nil, d2)
	fmt.Println("missing baseline        ->", err)

	// Same canonical length, different content: {"name":"alice",...} vs tampered.
	tampered := map[string]any{
		"name": "alicf", // same length, different content
		"tags": []any{"a", "b", "c"},
		"meta": map[string]any{"age": 30, "city": "sh"},
	}
	_, _, err = gsbdiff.Apply(tampered, d2)
	fmt.Println("same-length tampered    ->", err)

	badWire := append([]byte(nil), wire...)
	badWire[44], badWire[45], badWire[46], badWire[47] = 0xff, 0xff, 0xff, 0xff
	_, err = gsbdiff.Decode(badWire)
	fmt.Println("huge declared opCount   ->", err)
	_, err = gsbdiff.Decode(wire[:len(wire)-3])
	fmt.Println("truncated stream        ->", err)
}

func must(step string, err error) {
	if err != nil {
		panic(step + ": " + err.Error())
	}
}
