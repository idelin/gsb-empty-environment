// Demo: 5-node Multi-Paxos cluster with a network partition, plus the
// zero-allocation cache and object pool on the hot path.
package main

import (
	"fmt"
	"sort"
	"time"

	"multipaxos/paxos"
	"multipaxos/pool"
)

func main() {
	c := paxos.NewCluster(5)
	quorum := 3
	entryPool := pool.NewEntryPool(128)
	cache := pool.NewCache(1024, 128, true)

	propose := func(l *paxos.Leader, v string) uint64 {
		e := entryPool.Acquire()
		defer entryPool.Release(e)
		e.Value = append(e.Value, v...)
		slot, err := l.Propose(e.Value)
		if err != nil {
			fmt.Printf("  propose %q failed: %v\n", v, err)
			return 0
		}
		_ = cache.Put(slot, e.Value)
		return slot
	}

	fmt.Println("== Phase A: node 0 leads, commits slots 1-3 ==")
	oldLeader := paxos.NewLeader(c.Node(0), c.Peers(0), c, quorum, time.Minute)
	for i := 1; i <= 3; i++ {
		propose(oldLeader, fmt.Sprintf("stable-%d", i))
	}

	fmt.Println("== Phase B: partition {0,1} | {2,3,4}; old leader writes dirty slot 4 ==")
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	propose(oldLeader, "dirty-4") // fails: minority cannot commit

	fmt.Println("== Phase C: node 2 elected by majority, repairs log via Phase 1 ==")
	newLeader := paxos.NewLeader(c.Node(2), c.Peers(2), c, quorum, time.Minute)
	newLeader.BumpBallot(oldLeader.Ballot().N) // election handoff: start above old leader
	propose(newLeader, "real-4")
	propose(newLeader, "real-5")

	fmt.Println("== Phase D: heal; stale leader rejected; new leader overwrites dirty logs ==")
	c.Heal()
	propose(oldLeader, "zombie") // rejected: majority promised a higher ballot
	newLeader.Refresh()          // re-run Phase 1 across all 5 nodes
	propose(newLeader, "real-6") // repairs + overwrites dirty minority logs

	fmt.Println("\n== Final committed log (node 1, formerly partitioned minority) ==")
	slots := c.Node(1).DecidedSlots()
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, slot := range slots {
		e, _ := c.Node(1).Decided(slot)
		v := string(e.Value)
		if e.Noop {
			v = "<noop>"
		}
		fmt.Printf("  slot %d -> %s\n", slot, v)
	}

	delivered, dropped := c.Stats()
	hits, misses, _ := cache.Stats()
	fmt.Printf("\ntransport: %d delivered, %d dropped (partition)\n", delivered, dropped)
	fmt.Printf("cache: %d hits, %d misses; pool outstanding=%d (0 = no leaks)\n",
		hits, misses, entryPool.Outstanding())
}
