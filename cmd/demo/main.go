// Command demo builds a full B+ tree of 500 records, deletes them one by one
// down to 3, and prints how the tree height shrinks from 4 levels to 1 while
// merged pages are reclaimed into the free page list.
package main

import (
	"fmt"

	"bptree"
)

func main() {
	tr := bptree.New()
	for i := 1; i <= 500; i++ {
		tr.Insert(i)
	}
	st := tr.Stats()
	fmt.Printf("built full tree: keys=500 height=%d livePages=%d freePages=%d\n",
		tr.Height(), st.LivePages, st.FreePages)

	prev := tr.Height()
	for i := 1; i <= 497; i++ {
		tr.Delete(i)
		if h := tr.Height(); h != prev {
			st = tr.Stats()
			fmt.Printf("deleted %3d keys: height %d -> %d (livePages=%d freePages=%d)\n",
				i, prev, h, st.LivePages, st.FreePages)
			prev = h
		}
	}

	st = tr.Stats()
	fmt.Printf("final: remaining=%v height=%d livePages=%d freePages=%d created=%d mergedFreed=%d\n",
		tr.Scan(), tr.Height(), st.LivePages, st.FreePages, st.Created, st.MergedFree)
}
