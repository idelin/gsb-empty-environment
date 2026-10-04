// Minimal end-to-end demo of the copy-on-write B+tree: snapshot isolation,
// range scans, validation, reclamation, and page-file corruption detection.
package main

import (
	"fmt"

	"bptree"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	tree, err := bptree.New(bptree.Options{Capacity: 4, MaxValueSize: 64})
	check(err)

	// 1. Load some data in one write transaction.
	tx := tree.Begin()
	for k := int64(0); k < 20; k++ {
		check(tx.Put(k, []byte(fmt.Sprintf("v%02d", k))))
	}
	check(tx.Commit())
	fmt.Println("loaded keys 0..19, root version:", tree.Version())

	// 2. Pin a snapshot, then mutate the tree (overwrite + delete + insert).
	snap := tree.Snapshot()
	tx = tree.Begin()
	check(tx.Put(7, []byte("OVERWRITTEN")))
	check(tx.Delete(3))
	for k := int64(100); k < 130; k++ {
		check(tx.Put(k, []byte("new")))
	}
	check(tx.Commit())
	fmt.Println("committed overwrite/delete/insert, root version:", tree.Version())

	// 3. The snapshot still serves the old version; the tree serves the new.
	v, _ := snap.Get(7)
	fmt.Printf("snapshot sees key 7  = %q (old value stable)\n", v)
	v, _ = tree.Get(7)
	fmt.Printf("live tree sees key 7 = %q\n", v)
	if _, err := snap.Get(3); err == nil {
		fmt.Println("snapshot still has deleted key 3")
	}
	if _, err := tree.Get(3); err == bptree.ErrNotFound {
		fmt.Println("live tree: key 3 is deleted")
	}

	// 4. Range scan [10, 15) on the snapshot: left-closed, right-open.
	fmt.Print("snapshot scan [10,15):")
	it := snap.Iterator(10, 15)
	for it.Next() {
		fmt.Printf(" %d=%s", it.Key(), it.Value())
	}
	check(it.Err())
	fmt.Println()

	// 5. Independent structural validation of current and historical roots.
	check(tree.Validate())
	check(bptree.Validate(tree.Store(), snap.Root(), 4))
	fmt.Println("validator: current and snapshot roots are well-formed")

	// 6. Reclamation: snapshot pages survive, everything else is recycled.
	freed, _ := tree.Collect()
	fmt.Printf("GC with open snapshot: reclaimed %d pages\n", freed)
	snap.Close()
	freed, _ = tree.Collect()
	fmt.Printf("GC after snapshot close: reclaimed %d more pages\n", freed)

	// 7. Page-file corruption check.
	victim := tree.Root()
	tree.Store().Corrupt(victim)
	fmt.Println("corrupt pages:", tree.Store().CheckIntegrity())
	if err := tree.Validate(); err != nil {
		fmt.Println("validator on corrupt tree:", err)
	}
}
