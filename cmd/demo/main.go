// Command demo exercises the sparseidx store end to end: appends, tombstones,
// patches, sparse-index k-th lookups vs full decode, snapshots, compaction,
// garbage collection and crash recovery.
package main

import (
	"fmt"
	"log"
	"os"

	"sparseidx"
)

func main() {
	dir, err := os.MkdirTemp("", "sparseidx-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	st, err := sparseidx.Open(dir, &sparseidx.Options{Stride: 4})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("== append variable-length records (3 blocks) ==")
	for i := 0; i < 12; i++ {
		key := fmt.Sprintf("user:%02d", i)
		val := fmt.Sprintf("{\"name\":\"user-%d\",\"bio\":\"%s\"}", i, string(make([]byte, i)))
		if err := st.Put([]byte(key), []byte(val)); err != nil {
			log.Fatal(err)
		}
		if i%4 == 3 {
			if err := st.Flush(); err != nil {
				log.Fatal(err)
			}
		}
	}
	fmt.Printf("committed records: %d\n", st.NumRecords())

	fmt.Println("\n== patch + tombstone ==")
	must(st.Patch([]byte("user:01"), 9, []byte("anon-1"))) // replaces "user-1"
	must(st.Delete([]byte("user:02")))
	must(st.Flush())
	if v, ok := st.Get([]byte("user:01")); ok {
		fmt.Printf("user:01 after patch: %s\n", v)
	}
	if _, ok := st.Get([]byte("user:02")); !ok {
		fmt.Println("user:02 after tombstone: <absent>")
	}

	fmt.Println("\n== k-th lookup via sparse index vs full decode ==")
	for _, k := range []uint64{0, 5, 9, 13} {
		rec, err := st.Kth(k)
		if err != nil {
			log.Fatal(err)
		}
		slow, err := st.KthSlow(k)
		if err != nil {
			log.Fatal(err)
		}
		match := string(rec.Key) == string(slow.Key)
		fmt.Printf("k=%2d key=%-8q decoded=%d (<= stride 4) matches-full-decode=%v\n",
			k, rec.Key, st.LastSeekDecoded(), match)
	}

	fmt.Println("\n== snapshot + compact + gc ==")
	snap := st.Snapshot()
	must(st.Compact())
	must(st.CollectGarbage())
	if v, ok, err := snap.Get([]byte("user:01")); err == nil && ok {
		fmt.Printf("snapshot still sees pre-compact user:01: %s\n", v)
	}
	snap.Close()
	must(st.CollectGarbage())
	entries, _ := os.ReadDir(dir + "/blocks")
	fmt.Printf("blocks on disk after gc: %d\n", len(entries))

	fmt.Println("\n== crash recovery: reopen from disk ==")
	st2, err := sparseidx.Open(dir, &sparseidx.Options{Stride: 4})
	if err != nil {
		log.Fatal(err)
	}
	rec, err := st2.Kth(9)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("reopened: records=%d recovered-blocks=%d k=9 key=%q\n",
		st2.NumRecords(), st2.RecoveredBlocks, rec.Key)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
