// Command demo runs the acceptance scenario end to end:
//
//	Phase 1: a 5-node cluster elects node0 and commits entries under a
//	         leader lease (Phase 1 of Paxos is skipped on the happy path).
//	Phase 2: the cluster partitions {0,1} | {2,3,4}; the old leader's
//	         writes fail (no majority) and stay dirty on the minority.
//	Phase 3: node2 wins a majority election, repairs holes via Phase 1
//	         and commits new entries.
//	Phase 4: the partition heals; a repair campaign backfills the
//	         minority and safely overwrites its dirty log.
//
// It then demonstrates the multi-tenant DRR scheduler with weighted
// fairness and quota isolation.
package main

import (
	"fmt"
	"time"

	"multipaxos/paxos"
	"multipaxos/scheduler"
)

func showLog(label string, nodes []*paxos.Node) {
	fmt.Printf("  %-14s", label)
	for _, n := range nodes {
		fmt.Printf(" n%d=%v", n.ID(), n.Committed())
	}
	fmt.Println()
}

func main() {
	fmt.Println("=== Multi-Paxos: partition & recovery scenario ===")
	net := paxos.NewNetwork()
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(i, 5, time.Hour)
		net.Attach(nodes[i])
	}

	fmt.Println("[1] node0 campaigns and commits v0,v1 under its leader lease")
	nodes[0].Campaign()
	nodes[0].Propose("v0")
	nodes[0].Propose("v1")
	prepares, accepts, _ := net.Stats()
	fmt.Printf("    messages so far: prepares=%d accepts=%d (lease bypasses Phase 1)\n", prepares, accepts)
	showLog("committed:", nodes)

	fmt.Println("[2] partition {0,1} | {2,3,4}; old leader node0 keeps writing")
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := nodes[0].Propose("dirty"); err != nil {
		fmt.Printf("    old leader propose rejected: %v (dirty slot stays uncommitted)\n", err)
	}
	fmt.Printf("    node0 raw log: %v  <- dirty on minority only\n", nodes[0].Log())

	fmt.Println("[3] node2 wins majority election, repairs log, commits v2,v3")
	nodes[2].Campaign()
	nodes[2].Propose("v2")
	nodes[2].Propose("v3")
	showLog("committed:", nodes)

	fmt.Println("[4] heal; repair campaign backfills minority, overwrites dirty slot")
	net.Heal()
	nodes[2].Campaign()
	showLog("committed:", nodes)
	fmt.Printf("    node0 raw log after repair: %v (dirty value gone)\n", nodes[0].Log())
	fmt.Printf("    node0.IsLeader=%v node2.IsLeader=%v\n", nodes[0].IsLeader(), nodes[2].IsLeader())

	fmt.Println()
	fmt.Println("=== Multi-tenant DRR scheduler ===")
	s := scheduler.New(1)
	s.AddTenant("analytics", 3, 4, 1000) // weight 3, in-flight quota 4
	s.AddTenant("billing", 2, 4, 1000)   // weight 2
	s.AddTenant("batch", 1, 4, 1000)     // weight 1

	for i := 0; i < 1000; i++ {
		for _, name := range []string{"analytics", "billing", "batch"} {
			if err := s.Submit(name, scheduler.Job{ID: int64(i)}); err != nil {
				panic(err)
			}
		}
	}
	counts := make(map[string]int)
	for i := 0; i < 600; i++ { // dispatch a window while all are backlogged
		name, _, ok := s.Dispatch()
		if !ok {
			break
		}
		counts[name]++
		s.Done(name)
	}
	fmt.Printf("  weights 3:2:1 -> dispatched in window: %v\n", counts)

	// Quota isolation: "analytics" may hold at most 4 in-flight jobs.
	isolated := scheduler.New(1)
	isolated.AddTenant("hog", 100, 2, 1000) // huge weight, tiny quota
	isolated.AddTenant("app", 1, 100, 1000)
	for i := 0; i < 10; i++ {
		isolated.Submit("hog", scheduler.Job{})
		isolated.Submit("app", scheduler.Job{})
	}
	inflight := make(map[string]int)
	for i := 0; i < 12; i++ {
		name, _, ok := isolated.Dispatch()
		if !ok {
			break
		}
		inflight[name]++
		if name == "app" { // only "app" completes work; "hog" stalls at quota
			isolated.Done(name)
		}
	}
	st := isolated.Stats()
	fmt.Printf("  quota isolation: hog capped at 2 in-flight -> stats %+v\n", st)
}
