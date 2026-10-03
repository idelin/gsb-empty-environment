// Command demo builds a WAL with five transactions (two committed, three
// uncommitted), checkpoints, simulates a crash, and runs ARIES recovery.
// It also demonstrates locality-aware shard routing.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"ariesdb/aries"
	"ariesdb/shard"
	"ariesdb/wal"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func appendUpdate(w *wal.Writer, txnID, pageID uint64, prev wal.LSN, before, after string) wal.LSN {
	lsn, err := w.Append(&wal.Record{
		Type: wal.TypeUpdate, TxnID: txnID, PrevLSN: prev, PageID: pageID,
		Before: []byte(before), After: []byte(after),
	})
	must(err)
	return lsn
}

func main() {
	dir, err := os.Getwd()
	must(err)
	path := filepath.Join(dir, "demo.log")
	os.Remove(path)

	fmt.Println("=== Phase 0: build WAL (T1,T2 committed; T3,T4,T5 uncommitted) ===")
	w, err := wal.Open(path)
	must(err)
	t1u := appendUpdate(w, 1, 1, 0, "p1-v0", "p1-t1")
	t2u := appendUpdate(w, 2, 2, 0, "p2-v0", "p2-t2")
	t1c, _ := w.Append(&wal.Record{Type: wal.TypeCommit, TxnID: 1, PrevLSN: t1u})
	t2c, _ := w.Append(&wal.Record{Type: wal.TypeCommit, TxnID: 2, PrevLSN: t2u})
	_, err = w.Append(&wal.Record{
		Type: wal.TypeCheckpoint,
		Checkpoint: &wal.CheckpointData{
			Txns: map[uint64]wal.TxnSnapshot{
				1: {LastLSN: t1c, Status: wal.TxnCommitted},
				2: {LastLSN: t2c, Status: wal.TxnCommitted},
			},
			DirtyPages: map[uint64]wal.LSN{1: t1u, 2: t2u},
		},
	})
	must(err)
	t3u := appendUpdate(w, 3, 1, 0, "p1-t1", "p1-t3")
	t4u := appendUpdate(w, 4, 3, 0, "p3-v0", "p3-t4")
	appendUpdate(w, 5, 4, 0, "p4-v0", "p4-t5")
	must(w.Close())

	recs, err := wal.ReadAll(path)
	must(err)
	for _, r := range recs {
		fmt.Println("  WAL:", r)
	}

	fmt.Println("=== Crash! Disk state at crash time ===")
	store := aries.NewStorage()
	store.Apply(1, t3u, []byte("p1-t3")) // uncommitted, flushed
	store.Apply(2, 0, []byte("p2-v0"))   // committed, lost
	store.Apply(3, t4u, []byte("p3-t4")) // uncommitted, flushed
	store.Apply(4, 0, []byte("p4-v0"))   // uncommitted, lost
	for p := uint64(1); p <= 4; p++ {
		pg := store.Get(p)
		fmt.Printf("  page %d: lsn=%d data=%q\n", p, pg.LSN, pg.Data)
	}

	fmt.Println("=== ARIES recovery: Analysis -> Redo -> Undo ===")
	rec := aries.NewRecovery(path, store)
	must(rec.Analysis())
	fmt.Println("  DPT:", rec.DPT)
	for id, e := range rec.TxnTable {
		fmt.Printf("  txn %d: status=%s lastLSN=%d\n", id, e.Status, e.LastLSN)
	}
	must(rec.Redo())
	must(rec.Undo())
	for _, r := range rec.Appended {
		fmt.Println("  undo appended:", r)
	}

	fmt.Println("=== Final disk state ===")
	for p := uint64(1); p <= 4; p++ {
		pg := store.Get(p)
		fmt.Printf("  page %d: lsn=%d data=%q\n", p, pg.LSN, pg.Data)
	}
	fmt.Println("  loser transactions remaining:", len(rec.TxnTable))

	fmt.Println("=== Locality-aware shard routing ===")
	router := shard.NewRouter(1024, 3)
	for z := 0; z < 3; z++ {
		for n := 0; n < 4; n++ {
			router.AddNode(&shard.Node{
				ID:   shard.NodeID(fmt.Sprintf("node-z%d-%d", z, n)),
				Zone: shard.ZoneID(fmt.Sprintf("zone-%d", z)),
			})
		}
	}
	for _, key := range []string{"user:1001", "user:1002", "order:77"} {
		for z := 0; z < 3; z++ {
			zone := shard.ZoneID(fmt.Sprintf("zone-%d", z))
			node, sh, err := router.Route(key, zone)
			must(err)
			fmt.Printf("  key=%-10s client=%s -> shard=%d node=%s\n", key, zone, sh, node)
		}
	}
	fmt.Println("  primary distribution:", router.Distribution())
}
