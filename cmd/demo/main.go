// Command demo runs a 5-node Multi-Paxos cluster through the full
// acceptance scenario: election, lease fast-path commits, a network
// partition that strands the old leader, majority-side re-election with
// Phase 1 repair, and safe overwrite of the minority's dirty log after
// healing.
package main

import (
	"fmt"
	"sync"
	"time"

	"multipaxos/paxos"
)

type stateMachine struct {
	mu  sync.Mutex
	log []string
}

func (s *stateMachine) apply(idx uint64, e paxos.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Noop {
		s.log = append(s.log, fmt.Sprintf("#%d <noop>", idx))
	} else {
		s.log = append(s.log, fmt.Sprintf("#%d %s", idx, e.Value))
	}
}

func (s *stateMachine) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("%v", s.log)
}

func main() {
	const n = 5
	tr := paxos.NewTransport(n)
	nodes := make([]*paxos.Node, n)
	sms := make([]*stateMachine, n)
	for i := 0; i < n; i++ {
		sms[i] = &stateMachine{}
		nodes[i] = paxos.NewNode(i, n, tr, sms[i].apply, 5*time.Second)
	}
	defer func() {
		for _, nd := range nodes {
			nd.Close()
		}
	}()

	fmt.Println("=== 1. Node 0 wins election (Phase 1: Prepare/Promise) ===")
	if !nodes[0].Campaign() {
		panic("campaign failed")
	}
	fmt.Printf("leader=0  prepares=%d\n\n", tr.Prepares.Load())

	fmt.Println("=== 2. Lease fast path: Phase 1 bypassed for steady-state commits ===")
	must(nodes[0].Propose([]byte("set x=1")))
	must(nodes[0].Propose([]byte("set y=2")))
	fmt.Printf("committed 2 entries; prepares=%d (unchanged), accepts=%d\n\n",
		tr.Prepares.Load(), tr.Accepts.Load())

	fmt.Println("=== 3. Network partition {0,1} | {2,3,4}: old leader stranded ===")
	tr.SetPartition([]int{0, 1}, []int{2, 3, 4})
	err := nodes[0].Propose([]byte("set z=3-dirty"))
	fmt.Printf("old leader propose -> %v (dirty entry left on minority)\n", err)
	fmt.Printf("old leader commitIndex=%d (cannot advance without quorum)\n\n", nodes[0].CommitIndex())

	fmt.Println("=== 4. Majority side elects node 2; Phase 1 repairs the log ===")
	if !nodes[2].Campaign() {
		panic("majority campaign failed")
	}
	must(nodes[2].Propose([]byte("set z=3")))
	fmt.Printf("new leader=2 commitIndex=%d\n\n", nodes[2].CommitIndex())

	fmt.Println("=== 5. Heal: minority catches up, dirty slot safely overwritten ===")
	tr.Heal()
	must(nodes[2].Propose([]byte("set w=4")))
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < n; i++ {
		fmt.Printf("node %d: commit=%d applied=%d leader=%v log=%s\n",
			i, nodes[i].CommitIndex(), nodes[i].Applied(), nodes[i].IsLeader(), sms[i])
	}
	e, _ := nodes[0].LogEntry(3)
	fmt.Printf("\nnode 0 slot 3 now holds %q (dirty value overwritten by committed value)\n", e.Value)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
