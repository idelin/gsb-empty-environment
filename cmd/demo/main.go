// Command demo runs the ARIES crash-recovery acceptance scenario and a
// locality-aware routing showcase.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"ariesdb/aries"
	"ariesdb/router"
)

func main() {
	demoRecovery()
	fmt.Println()
	demoRouter()
}

func demoRecovery() {
	fmt.Println("=== ARIES crash recovery demo ===")
	dir, err := os.MkdirTemp("", "aries-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "wal.log")
	disk := aries.NewDisk()
	wal := aries.NewWAL(walPath)
	db := aries.NewDatabase(wal, disk)

	fmt.Println("-- workload: T1,T2 commit; T3,T4,T5 stay uncommitted; crash after checkpoint")
	db.Update(1, 10, []byte("a0"), []byte("a1"))
	db.Update(2, 20, []byte("b0"), []byte("b1"))
	db.Update(3, 30, []byte("c0"), []byte("c1"))
	db.Commit(1)
	db.Update(4, 40, []byte("d0"), []byte("d1"))
	db.BP.FlushPage(10)
	db.Checkpoint()
	db.Update(2, 20, []byte("b1"), []byte("b2"))
	db.Commit(2)
	db.Update(5, 50, []byte("e0"), []byte("e1"))
	db.Update(3, 10, []byte("a1"), []byte("a2"))
	db.Checkpoint()
	db.BP.FlushPage(20)
	db.Update(4, 40, []byte("d1"), []byte("d2"))
	db.Update(5, 30, []byte("c1"), []byte("c3"))
	if err := wal.Flush(); err != nil {
		panic(err)
	}
	fmt.Printf("-- CRASH: WAL durable at %s, buffer pool lost\n", walPath)

	wal2, err := aries.LoadWAL(walPath)
	if err != nil {
		panic(err)
	}
	stats := aries.Recover(wal2, disk)

	fmt.Printf("-- analysis: TT=%v DPT=%v\n", formatTT(stats.AnalysisTT), formatDPT(stats.AnalysisDPT))
	fmt.Printf("-- redo: repeated %d log records\n", len(stats.Redone))
	fmt.Printf("-- undo: losers=%v, wrote %d CLRs\n", stats.Losers, len(stats.CLRs))
	for _, page := range []aries.PageID{10, 20, 30, 40, 50} {
		p := disk.ReadPage(page)
		fmt.Printf("   page %-3d -> %q (pageLSN=%d)\n", page, p.Data, p.PageLSN)
	}
}

func formatTT(tt map[aries.TxID]aries.TTEntry) string {
	txs := make([]int, 0, len(tt))
	for tx := range tt {
		txs = append(txs, int(tx))
	}
	sort.Ints(txs)
	out := "{"
	for i, tx := range txs {
		if i > 0 {
			out += " "
		}
		e := tt[aries.TxID(tx)]
		out += fmt.Sprintf("T%d:%s@%d", tx, e.Status, e.LastLSN)
	}
	return out + "}"
}

func formatDPT(dpt map[aries.PageID]aries.LSN) string {
	pages := make([]int, 0, len(dpt))
	for p := range dpt {
		pages = append(pages, int(p))
	}
	sort.Ints(pages)
	out := "{"
	for i, p := range pages {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("P%d:recLSN=%d", p, dpt[aries.PageID(p)])
	}
	return out + "}"
}

func demoRouter() {
	fmt.Println("=== Hierarchical sharding & locality-aware routing demo ===")
	r, err := router.NewRouter(1024, 3)
	if err != nil {
		panic(err)
	}
	regions := []string{"cn-east", "cn-north", "us-west"}
	for i := 0; i < 12; i++ {
		loc := router.Locality{
			Region: regions[i%3],
			Zone:   fmt.Sprintf("%s-z%d", regions[i%3], i/3),
		}
		if err := r.AddNode(router.NodeID(fmt.Sprintf("node-%02d", i)), loc); err != nil {
			panic(err)
		}
	}
	fmt.Printf("-- cluster: %d nodes in %d regions, %d virtual shards, replication=3\n",
		r.NodeCount(), len(regions), r.ShardCount())

	client := router.Locality{Region: "cn-east", Zone: "cn-east-z0"}
	loads := map[router.NodeID]int{}
	zoneLocal := 0
	regionLocal := 0
	const keys = 10000
	for i := 0; i < keys; i++ {
		id, _, err := r.Route(fmt.Sprintf("user:%d", i), client)
		if err != nil {
			panic(err)
		}
		loads[id]++
		loc, err := r.LocalityOf(id)
		if err != nil {
			panic(err)
		}
		if loc.Zone == client.Zone && loc.Region == client.Region {
			zoneLocal++
		} else if loc.Region == client.Region {
			regionLocal++
		}
		r.Done(id)
	}
	fmt.Printf("-- routed %d keys from client at %s/%s: zone-local=%d region-local=%d remote=%d\n",
		keys, client.Region, client.Zone, zoneLocal, regionLocal, keys-zoneLocal-regionLocal)
	for i := 0; i < 12; i++ {
		id := router.NodeID(fmt.Sprintf("node-%02d", i))
		fmt.Printf("   %-8s hits=%d\n", id, loads[id])
	}

	before := map[int]router.NodeID{}
	for s := 0; s < r.ShardCount(); s++ {
		reps, err := r.Replicas(s)
		if err != nil {
			panic(err)
		}
		before[s] = reps[0]
	}
	if err := r.AddNode("node-12", router.Locality{Region: "cn-east", Zone: "cn-east-z9"}); err != nil {
		panic(err)
	}
	moved := 0
	for s := 0; s < r.ShardCount(); s++ {
		reps, err := r.Replicas(s)
		if err != nil {
			panic(err)
		}
		if reps[0] != before[s] {
			moved++
		}
	}
	fmt.Printf("-- scale-out +1 node: primary moved for %d/%d shards (%.1f%%)\n",
		moved, r.ShardCount(), 100*float64(moved)/float64(r.ShardCount()))
}
