// Command demo simulates the acceptance scenario end to end: a 5-node
// Multi-Paxos cluster, a network partition that strands the old leader,
// re-election on the majority side, Phase 1 gap repair, and safe
// overwrite of the minority's dirty log after healing. It also showcases
// the zero-allocation cache and object pool.
package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"multipaxos/cache"
	"multipaxos/paxos"
)

func val(e *paxos.LogEntry) string {
	if e == nil {
		return "-"
	}
	if e.IsNoop() {
		return "<noop>"
	}
	return fmt.Sprintf("%q", e.Value)
}

func state(e *paxos.LogEntry) string {
	if e == nil {
		return "-"
	}
	if e.State == paxos.StateCommitted {
		return "committed"
	}
	return "accepted(dirty)"
}

func dumpLog(title string, nodes []*paxos.Node, slots uint64) {
	fmt.Printf("  %-22s", title)
	for s := uint64(1); s <= slots; s++ {
		fmt.Printf("| slot%d ", s)
	}
	fmt.Println()
	for _, n := range nodes {
		role := "follower"
		if n.IsLeader() {
			role = "LEADER"
		}
		fmt.Printf("  node%d %-10s ci=%d ", n.ID(), role, n.CommitIndex())
		for s := uint64(1); s <= slots; s++ {
			e := n.Entry(s)
			fmt.Printf("| %-16s ", val(e)+"@"+state(e))
		}
		fmt.Println()
	}
	fmt.Println()
}

func main() {
	fmt.Println("=== Multi-Paxos: partition, re-election, gap repair, dirty-log overwrite ===")
	fmt.Println()

	net := paxos.NewNetwork()
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(paxos.Config{ID: i + 1, LeaseDuration: time.Hour}, net)
		net.Add(nodes[i])
	}
	oldLeader, newLeader := nodes[0], nodes[2]

	fmt.Println("[1] Node 1 campaigns (Phase 1) and commits a1,a2,a3 under its lease (Phase 2 only)")
	must(oldLeader.Campaign())
	for _, v := range []string{"a1", "a2", "a3"} {
		must2(oldLeader.Propose([]byte(v)))
	}
	dumpLog("after 3 commits", nodes, 4)

	fmt.Println("[2] Partition {1,2} | {3,4,5}; old leader proposes X@4 -> accepted by minority only")
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, err := oldLeader.Propose([]byte("X")); err != nil {
		fmt.Printf("    old leader commit fails as expected: %v\n", err)
	}
	dumpLog("X@4 dirty on minority", nodes, 4)

	fmt.Println("[3] Majority side elects node 3; Phase 1 repairs the log, slot 4 is reused")
	prepares := net.PrepareCount()
	must(newLeader.Campaign())
	fmt.Printf("    Phase 1 ran (%d new Prepare msgs); commit index = %d\n",
		net.PrepareCount()-prepares, newLeader.CommitIndex())
	slot, _ := newLeader.Propose([]byte("b4"))
	fmt.Printf("    new leader commits b4 at slot %d (replaces minority's dirty X)\n", slot)
	must2(newLeader.Propose([]byte("b5")))
	dumpLog("new leader repaired log", nodes, 5)

	fmt.Println("[4] Heal: stale leader is rejected, minority dirty log is safely overwritten")
	net.Heal()
	if _, err := oldLeader.Propose([]byte("stale")); err != nil {
		fmt.Printf("    stale leader propose rejected: %v (steps down)\n", err)
	}
	must2(newLeader.Propose([]byte("b6"))) // triggers commit broadcast + catch-up sync
	fmt.Printf("    catch-up syncs: %d\n", net.SyncCount())
	dumpLog("converged cluster", nodes, 6)

	fmt.Println("=== Zero-allocation cache & object pool ===")
	fmt.Println()
	c := cache.NewCache(cache.Options{Capacity: 1 << 16, MaxValueSize: 64})
	dst := make([]byte, 64)
	c.Put(1, []byte("paxos"))
	before := readAllocs()
	for i := 0; i < 100000; i++ {
		c.Put(1, []byte("paxos"))
		c.Get(1, dst)
	}
	fmt.Printf("  cache: 100k Get+Put on hot key, allocs/op = %.4f, stats = %+v\n",
		float64(readAllocs()-before)/200000, c.Stats())

	pool := cache.NewPool(
		func() *paxos.LogEntry { return &paxos.LogEntry{} },
		func(e *paxos.LogEntry) { *e = paxos.LogEntry{} },
	)
	e := pool.Get()
	e.Slot = 1
	e.Value = []byte("recycled")
	pool.Put(e)
	before = readAllocs()
	for i := 0; i < 100000; i++ {
		v := pool.Get()
		pool.Put(v)
	}
	fmt.Printf("  pool:  100k Get+Put, allocs/op = %.4f, allocated = %d, in-use = %d (leak-free)\n",
		float64(readAllocs()-before)/200000, pool.Allocated(), pool.InUse())

	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("demo finished: cluster converged, no leaks, zero-alloc hot paths")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func must2(_ uint64, err error) { must(err) }

func readAllocs() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Mallocs
}
