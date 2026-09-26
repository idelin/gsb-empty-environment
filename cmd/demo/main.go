package main

import (
	"fmt"

	"pathtree"
)

func main() {
	empty := pathtree.New()

	updates := []pathtree.Update{
		{Key: "d", Value: []byte("delta")},
		{Key: "b", Value: []byte("bravo")},
		{Key: "f", Value: []byte("foxtrot")},
		{Key: "a", Value: []byte("alpha")},
		{Key: "c", Value: []byte("charlie")},
		{Key: "e", Value: []byte("echo")},
		{Key: "g", Value: []byte("golf")},
	}
	v1, stats := empty.Batch(updates)

	fmt.Println("v1 digest:", v1.DigestString())
	fmt.Println("v1 len:", v1.Len())
	fmt.Printf("v1 batch nodes: copied=%d built=%d new=%d\n", stats.CopiedNodes, stats.BuiltNodes, stats.NewNodes)

	rankC, _ := v1.Rank("c")
	key, value, _ := v1.Select(4)
	fmt.Printf("rank(c)=%d select(4)=%s:%s\n", rankC, key, value)

	batchUpdate := []pathtree.Update{
		{Key: "b", Value: []byte("BRAVO-2")},
		{Key: "c", Delete: true},
		{Key: "d", Value: []byte("DELTA-2")},
		{Key: "missing", Delete: true},
	}
	v2, stats2 := v1.Batch(batchUpdate)

	fmt.Println("v2 digest:", v2.DigestString())
	fmt.Printf("v2 batch nodes: copied=%d built=%d new=%d absent-deletes=%d present-deletes=%d\n",
		stats2.CopiedNodes, stats2.BuiltNodes, stats2.NewNodes, stats2.AbsentDeletes, stats2.PresentDeletes)
	fmt.Println("old v1 digest still:", v1.DigestString())

	fmt.Print("range [b,d] on v2: ")
	rangeIt := v2.Range("b", "d", true, true)
	first := true
	for rangeIt.Next() {
		if !first {
			fmt.Print(" ")
		}
		fmt.Printf("%s=%s", rangeIt.Key(), rangeIt.Value())
		first = false
	}
	fmt.Println()

	oldIt := v1.Iterator()
	newIt := v2.Iterator()
	oldIt.Next()
	newIt.Next()
	newIt.Next()
	fmt.Printf("independent iterators: v1=%s v2=%s\n", oldIt.Key(), newIt.Key())

	layout := pathtree.NodeLayout()
	fmt.Printf("node header: %d bytes (%s)\n", layout.Size, layout.TypeName)
	for _, field := range layout.Fields {
		fmt.Printf("  %-6s offset=%d size=%d\n", field.Name, field.Offset, field.Size)
	}

	f1 := v1.LogicalFootprint()
	f2 := v2.LogicalFootprint()
	fmt.Printf("footprint v1: nodes=%d nodeBytes=%d keyBytes=%d valueBytes=%d total=%d\n",
		f1.UniqueNodes, f1.NodeBytes, f1.KeyBytes, f1.ValueBytes, f1.TotalBytes)
	fmt.Printf("footprint v2: nodes=%d nodeBytes=%d keyBytes=%d valueBytes=%d total=%d\n",
		f2.UniqueNodes, f2.NodeBytes, f2.KeyBytes, f2.ValueBytes, f2.TotalBytes)
}
