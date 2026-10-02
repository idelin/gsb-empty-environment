// Command cidxdemo demonstrates the compact index: real encoded layout and
// byte occupancy, queries served from the mapped bytes, and the mapping
// lease lifecycle (shared views, deferred close, generation isolation).
package main

import (
	"fmt"
	"strings"

	"compactidx"
)

func main() {
	// ---- Build a real encoded image -------------------------------------
	const n = 5000
	const chunk = 64
	kvs := make([]compactidx.KV, n)
	for i := range kvs {
		val := fmt.Sprintf("value-%05d", i)
		if i%10 == 0 { // long values exercise continuation chains
			val += strings.Repeat(fmt.Sprintf("-%d", i), 40)
		}
		kvs[i] = compactidx.KV{Key: fmt.Sprintf("key-%05d", i), Value: val}
	}
	img, err := compactidx.Build(kvs, chunk)
	if err != nil {
		panic(err)
	}

	var naive int
	for _, kv := range kvs {
		naive += len(kv.Key) + len(kv.Value)
	}
	fmt.Printf("== Byte occupancy ==\n")
	fmt.Printf("entries:            %d\n", n)
	fmt.Printf("encoded image:      %d bytes\n", len(img))
	fmt.Printf("raw key+value:      %d bytes\n", naive)
	fmt.Printf("index overhead:     %d bytes (%.1f%% on top of raw data)\n",
		len(img)-naive, 100*float64(len(img)-naive)/float64(naive))
	fmt.Printf("bytes per entry:    %.1f encoded vs %.1f raw\n",
		float64(len(img))/n, float64(naive)/n)

	// ---- Real encoded layout ---------------------------------------------
	segs, err := compactidx.Describe(img)
	if err != nil {
		panic(err)
	}
	var nRec, nCont int
	for _, s := range segs {
		if s.Kind == "record" {
			nRec++
			if s.Label == "continuation" {
				nCont++
			}
		}
	}
	fmt.Printf("\n== Encoded layout (%d segments: header + table + %d records, %d continuations) ==\n",
		len(segs), nRec, nCont)
	for _, s := range segs[:6] {
		fmt.Printf("  [%7d,%7d)  %-6s  %s\n", s.Start, s.End, s.Kind, truncate(s.Label, 40))
	}
	fmt.Printf("  ... (%d more segments)\n", len(segs)-6)

	// ---- Load and query ---------------------------------------------------
	idx := compactidx.NewIndex()
	if err := idx.Load(img); err != nil {
		panic(err)
	}
	fmt.Printf("\n== Queries (binary search over the offset table, gen %d) ==\n", idx.Generation())
	v1, err := idx.View()
	if err != nil {
		panic(err)
	}
	for _, key := range []string{"key-00000", "key-02500", "key-04999", "key-99999"} {
		val, found, err := v1.Get(key)
		if err != nil {
			panic(err)
		}
		if found {
			fmt.Printf("  Get(%q) = %q\n", key, truncate(string(val), 48))
		} else {
			fmt.Printf("  Get(%q) = <not found>\n", key)
		}
	}

	// ---- Views share the mapping; closing one keeps the others -----------
	v2, err := idx.View()
	if err != nil {
		panic(err)
	}
	v1.Close()
	if val, found, _ := v2.Get("key-00001"); found {
		fmt.Printf("\n== Lifecycle ==\nclosed view1; view2 still serves: Get(\"key-00001\") = %q\n", truncate(string(val), 32))
	}

	// ---- Reload: old handles keep their generation ------------------------
	img2, err := compactidx.Build([]compactidx.KV{{Key: "key-00001", Value: "reloaded-value"}}, 0)
	if err != nil {
		panic(err)
	}
	if err := idx.Load(img2); err != nil {
		panic(err)
	}
	oldVal, _, _ := v2.Get("key-00001")
	v3, _ := idx.View()
	newVal, _, _ := v3.Get("key-00001")
	fmt.Printf("after reload: view2 (gen %d) reads %q, view3 (gen %d) reads %q\n",
		v2.Generation(), truncate(string(oldVal), 24), v3.Generation(), truncate(string(newVal), 24))
	fmt.Printf("mappings unmapped so far: %d (old gen still leased by view2)\n", idx.Unmapped())
	v2.Close()
	fmt.Printf("after view2.Close(): unmapped = %d\n", idx.Unmapped())

	// ---- Close is deferred until the last borrow is released -------------
	c, err := v3.Cursor()
	if err != nil {
		panic(err)
	}
	idx.Close()
	fmt.Printf("after idx.Close() with cursor open: unmapped = %d (deferred)\n", idx.Unmapped())
	if _, _, err := v3.Get("key-00001"); err != nil {
		fmt.Printf("query after close fails as required: %v\n", err)
	}
	c.Close()
	v3.Close()
	fmt.Printf("after last borrow released: unmapped = %d\n", idx.Unmapped())
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
