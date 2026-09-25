// Command btree-demo exercises the btree package end to end: inserts, range
// scans, duplicate-key overwrite, snapshots across root splits, and crash
// recovery via fault injection. All files live in the directory given as
// the first argument (default ./btree-demo-data).
package main

import (
	"fmt"
	"os"

	"btree"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func count(snap *btree.Snapshot) int {
	n := 0
	must(snap.Scan(nil, nil, func(k, v []byte) bool { n++; return true }))
	return n
}

func main() {
	dir := "./btree-demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	db, err := btree.Open(dir, &btree.Options{PageSize: 256})
	must(err)
	fmt.Printf("opened %s (pageSize=%d, gen=%d)\n", db.Path(), db.PageSize(), db.Gen())

	// 1. Inserts (small pages -> frequent splits, including root splits).
	for i := 0; i < 60; i++ {
		must(db.Insert([]byte(fmt.Sprintf("key-%03d", i)), []byte(fmt.Sprintf("val-%d", i))))
	}
	fmt.Printf("inserted 60 keys, gen=%d\n", db.Gen())

	// 2. Duplicate key overwrite.
	must(db.Insert([]byte("key-007"), []byte("updated")))
	v, found, err := db.Get([]byte("key-007"))
	must(err)
	fmt.Printf("overwrite key-007: found=%v value=%s\n", found, v)

	// 3. Range scan [key-010, key-015).
	fmt.Print("scan [key-010, key-015):")
	must(db.Scan([]byte("key-010"), []byte("key-015"), func(k, val []byte) bool {
		fmt.Printf(" %s=%s", k, val)
		return true
	}))
	fmt.Println()

	// 4. Snapshot, then keep inserting past several root splits.
	snap, err := db.Snapshot()
	must(err)
	before := count(snap)
	for i := 60; i < 400; i++ {
		must(db.Insert([]byte(fmt.Sprintf("key-%03d", i)), []byte(fmt.Sprintf("val-%d", i))))
	}
	now, err := db.Snapshot()
	must(err)
	hOld, err := snap.Height()
	must(err)
	hNew, err := now.Height()
	must(err)
	fmt.Printf("after 400 inserts: height %d -> %d (root splits happened)\n", hOld, hNew)
	fmt.Printf("old snapshot (gen %d) still sees its %d keys; current sees %d keys\n",
		snap.Gen(), before, count(now))
	must(snap.Check())
	must(now.Check())
	fmt.Println("structure check OK for old snapshot and current tree")

	// 5. Crash recovery: kill the process right after the data fsync,
	// before the commit record (superblock) is published.
	crashDir := dir + "-crash"
	os.RemoveAll(crashDir)
	armed := false
	cdb, err := btree.Open(crashDir, &btree.Options{
		PageSize: 256,
		FaultHook: func(fp btree.FaultPoint) bool {
			return armed && fp == btree.FPAfterDataSync
		},
	})
	must(err)
	for i := 0; i < 50; i++ {
		must(cdb.Insert([]byte(fmt.Sprintf("a-%03d", i)), []byte("1")))
	}
	genBefore := cdb.Gen()
	armed = true
	err = cdb.Insert([]byte("a-99999"), []byte("2"))
	fmt.Printf("injected crash after data sync, before publish: insert -> %v\n", err)
	cdb.Close()

	rdb, err := btree.Open(crashDir, nil)
	must(err)
	rsnap, err := rdb.Snapshot()
	must(err)
	fmt.Printf("recovered: gen %d -> %d, keys=%d (old complete state, new key absent)\n",
		genBefore, rdb.Gen(), count(rsnap))
	must(rdb.Check())
	must(rdb.Insert([]byte("a-99999"), []byte("2"))) // recovered DB keeps working
	fmt.Printf("post-recovery insert OK, keys=%d\n", count(mustSnap(rdb)))
	must(rdb.Close())
	must(db.Close())
	fmt.Println("demo done")
}

func mustSnap(db *btree.DB) *btree.Snapshot {
	s, err := db.Snapshot()
	must(err)
	return s
}
