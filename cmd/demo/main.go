// Demo of the persistent path-copying AVL tree.
package main

import (
	"fmt"

	"pct"
)

func main() {
	// Build a base version of 100k keys in one batch.
	base := pct.Tree{}
	kvs := make([]pct.KV, 100_000)
	for i := range kvs {
		kvs[i] = pct.KV{Key: int64(i * 2), Val: int64(i)}
	}
	base = base.BatchPut(kvs)
	st := base.Stats()
	fmt.Printf("base: %d nodes, height %d, %d B/node -> %d B total (%.1f MiB)\n",
		st.Nodes, st.Height, st.BytesPerNode, st.TotalBytes, float64(st.TotalBytes)/float64(1<<20))

	// A batch of 1000 updates shares paths instead of copying per key.
	upd := make([]pct.KV, 1000)
	for i := range upd {
		upd[i] = pct.KV{Key: int64(i * 173), Val: -1}
	}
	v2 := base.BatchPut(upd)
	fmt.Printf("batch of %d updates: %d/%d nodes shared with base (%.1f%%)\n",
		len(upd), pct.SharedNodes(base, v2), base.Len(),
		100*float64(pct.SharedNodes(base, v2))/float64(base.Len()))

	// Old root is stable.
	if v, _ := base.Get(0); v != 0 {
		panic("base mutated!")
	}
	fmt.Printf("old root stable: base.Get(0)=%d, v2.Get(0)=%d\n",
		must(base.Get(0)), must(v2.Get(0)))

	// Rank / select.
	fmt.Printf("v2.Rank(10000)=%d, v2.Select(5000)=(%d,val=%d)\n",
		v2.Rank(10000), sel(v2, 5000), must(v2.Get(sel(v2, 5000))))

	// Range iteration on two versions side by side: no interference.
	it1, it2 := base.Range(10, 16), v2.Range(10, 16)
	fmt.Print("base range [10,16]:")
	for it1.Next() {
		fmt.Printf(" %d=%d", it1.Key(), it1.Value())
	}
	fmt.Print("\nv2   range [10,16]:")
	for it2.Next() {
		fmt.Printf(" %d=%d", it2.Key(), it2.Value())
	}
	fmt.Println()

	// Deleting an absent key keeps the digest (and the root) identical.
	d := v2.Digest()
	v3 := v2.Delete(999_999)
	fmt.Printf("digest before=%016x after-delete-absent=%016x equal=%v\n",
		d, v3.Digest(), d == v3.Digest())
}

func must(v int64, ok bool) int64 {
	if !ok {
		panic("missing")
	}
	return v
}

func sel(t pct.Tree, i int) int64 {
	k, ok := t.Select(i)
	if !ok {
		panic("out of range")
	}
	return k
}
