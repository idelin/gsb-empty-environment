// Command cidxdemo builds a compact index, prints its real byte layout,
// runs queries, and demonstrates lease-based mapping lifetime.
package main

import (
	"fmt"

	"cidx"
)

func main() {
	entries := [][2]string{
		{"apple", "fruit"},
		{"banana", "yellow"},
		{"cherry", "red"},
		{"date", "sweet"},
		{"elderberry", "tart"},
	}

	var b cidx.Builder
	raw := 0
	for _, p := range entries {
		if err := b.Add([]byte(p[0]), []byte(p[1])); err != nil {
			panic(err)
		}
		raw += len(p[0]) + len(p[1])
	}
	data, err := b.Bytes()
	if err != nil {
		panic(err)
	}

	fmt.Println("== byte layout ==")
	lay, err := cidx.Check(data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("file size: %d bytes (raw key+value bytes: %d)\n", len(data), raw)
	tableBytes := uint64(lay.EntryCount+1) * 4
	fmt.Printf("  header   [0, 40)            40 bytes\n")
	fmt.Printf("  offtable [40, %d)          %d bytes (%d entries)\n",
		40+tableBytes, tableBytes, lay.EntryCount+1)
	fmt.Printf("  data     [%d, %d)         %d bytes (%d records)\n",
		lay.DataOffset, lay.FileSize, lay.FileSize-lay.DataOffset, lay.EntryCount)
	for i, r := range lay.Regions {
		fmt.Printf("  validated region %d: [%3d, %3d)  %d bytes\n", i, r.Start, r.End, r.End-r.Start)
	}
	fmt.Printf("  entries: %d, offsets: %v\n", lay.EntryCount, lay.Offsets)

	fmt.Println("== queries ==")
	s := cidx.NewStore()
	if err := s.Load(data); err != nil {
		panic(err)
	}
	v1, err := s.View()
	if err != nil {
		panic(err)
	}
	for _, key := range []string{"cherry", "apple", "zzz"} {
		val, err := v1.Query([]byte(key))
		if err == cidx.ErrNotFound {
			fmt.Printf("  %-12q -> <not found>\n", key)
		} else if err != nil {
			panic(err)
		} else {
			fmt.Printf("  %-12q -> %q\n", key, val)
		}
	}

	fmt.Println("== mapping lifetime ==")
	m1 := s.Mapping()
	v2, _ := s.View()
	c, _ := v2.Cursor()
	fmt.Printf("  gen %d: opened 2 views + 1 cursor\n", m1.Generation())

	// Reload swaps the mapping and requests the old one's close, which is
	// deferred until v1, v2 and c all let go.
	var b2 cidx.Builder
	if err := b2.Add([]byte("apple"), []byte("green")); err != nil {
		panic(err)
	}
	data2, err := b2.Bytes()
	if err != nil {
		panic(err)
	}
	if err := s.Load(data2); err != nil {
		panic(err)
	}
	fmt.Printf("  reloaded as gen %d; old gen %d torn down: %v (deferred)\n",
		s.CurrentGeneration(), m1.Generation(), m1.TornDown())

	old, _ := v1.Query([]byte("apple"))
	fmt.Printf("  old view still serves gen %d value: %q\n", v1.Generation(), old)
	v3, _ := s.View()
	cur, _ := v3.Query([]byte("apple"))
	fmt.Printf("  new view serves gen %d value: %q\n", v3.Generation(), cur)

	v1.Close()
	fmt.Printf("  closed view 1; old mapping torn down: %v\n", m1.TornDown())
	v2.Close()
	fmt.Printf("  closed view 2; old mapping torn down: %v (cursor still borrows)\n", m1.TornDown())
	c.Close()
	fmt.Printf("  closed cursor; old mapping torn down: %v\n", m1.TornDown())

	if _, err := v1.Query([]byte("apple")); err != nil {
		fmt.Printf("  query on closed view -> %v\n", err)
	}
	v3.Close()
	fmt.Println("OK")
}
