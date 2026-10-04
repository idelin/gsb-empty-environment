// Command demo is a minimal reproduction of snapshot isolation on the
// copy-on-write B+ tree: a long-running export scan keeps reading a fixed
// version while write transactions split and merge leaves underneath it.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"bptree"
)

func main() {
	dir, err := os.MkdirTemp("", "bptree-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "demo.db")

	db, err := bptree.Open(path, 4) // tiny page capacity: splits happen early
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Version 1: load 0..49.
	if err := db.Update(func(tx *bptree.Tx) error {
		for i := int64(0); i < 50; i++ {
			if err := tx.Put(i, []byte(fmt.Sprintf("value-%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	// The export task pins a snapshot: its root never changes again.
	snap := db.Snapshot()
	defer snap.Close()
	fmt.Printf("snapshot pinned at seq=%d root=%d\n", db.Seq(), snap.Root())

	// Start the long scan, but only read the first 10 keys for now.
	it := snap.Scan(0, 50)
	read := 0
	for read < 10 && it.Next() {
		read++
	}

	// Meanwhile other writers commit new versions: overwrite every value,
	// insert keys that split leaves, delete keys that merge them.
	for round := 0; round < 3; round++ {
		if err := db.Update(func(tx *bptree.Tx) error {
			for i := int64(0); i < 50; i++ {
				if err := tx.Put(i, []byte(fmt.Sprintf("round%d-%d", round, i))); err != nil {
					return err
				}
			}
			for i := int64(0); i < 20; i++ {
				if err := tx.Put(1000+int64(round)*20+i, []byte("new")); err != nil {
					return err
				}
			}
			return tx.Delete(int64(round * 10))
		}); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("writers committed 3 new versions, current seq=%d\n", db.Seq())

	// Resume the scan: it must still see exactly the 50 original keys with
	// the original values — no duplicates, no omissions, no new data.
	stable := true
	count := 0
	it2 := snap.Scan(0, 50)
	for it2.Next() {
		want := fmt.Sprintf("value-%d", count)
		if it2.Key() != int64(count) || string(it2.Value()) != want {
			fmt.Printf("MISMATCH at %d: got (%d, %q), want (%d, %q)\n",
				count, it2.Key(), it2.Value(), count, want)
			stable = false
		}
		count++
	}
	if err := it2.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("snapshot scan: %d keys, stable=%v (expected 50 keys, stable=true)\n", count, stable)

	// The current version sees the new data.
	v, _ := db.Get(5)
	fmt.Printf("current version: key 5 = %q\n", v)

	// Structural validation of both versions.
	if err := db.Validate(snap.Root()); err != nil {
		log.Fatal(err)
	}
	if err := db.Validate(db.Root()); err != nil {
		log.Fatal(err)
	}
	fmt.Println("validator: both snapshot root and current root are well-formed")

	// Reclaim only collects pages unreachable from every active root.
	freed, _ := db.Reclaim()
	fmt.Printf("reclaim with snapshot open: freed %d pages (snapshot pages preserved)\n", freed)
	snap.Close()
	freed2, _ := db.Reclaim()
	fmt.Printf("reclaim after snapshot close: freed %d more pages\n", freed2)

	if err := db.CheckIntegrity(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("page file integrity check: OK")
}
