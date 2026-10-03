// Command demo runs the acceptance scenario end to end: a 5-node cluster,
// a network partition that strands the old leader, a new majority leader
// whose Phase 1 completes uncommitted slots, and safe overwrite of the
// minority's dirty log after healing. It finishes with durable persistence
// replayed through the zero-copy mmap reader.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"multipaxos/paxos"
	"multipaxos/store"
)

func main() {
	dir, err := os.MkdirTemp("", "mpx-demo-*")
	must(err)
	defer os.RemoveAll(dir)

	net := paxos.NewNetwork()
	ids := []int{1, 2, 3, 4, 5}
	nodes := make(map[int]*paxos.Node, len(ids))
	for _, id := range ids {
		fp, err := paxos.NewFilePersister(filepath.Join(dir, fmt.Sprintf("node%d.log", id)))
		must(err)
		n, err := paxos.NewNode(id, ids, net, fp, 10*time.Second)
		must(err)
		defer n.Close()
		nodes[id] = n
	}

	fmt.Println("== 1. elect node1; slot1 commits cluster-wide ==")
	nodes[1].Campaign()
	mustLeader(nodes[1])
	propose(nodes[1], "set x=1")
	fmt.Printf("   node5 applied=%s\n", applied(nodes[5]))

	fmt.Println("== 2. ack links 2..5->1 blocked: slot2 accepted everywhere, uncommitted ==")
	for _, id := range ids[1:] {
		net.BlockLink(id, 1)
	}
	propose(nodes[1], "set a=2")
	fmt.Printf("   node1 commitIndex=%d (slot2 accepted but not committed)\n", nodes[1].CommitIndex())

	fmt.Println("== 3. partition {1,2}|{3,4,5}; old leader dirties slot3 on minority ==")
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	propose(nodes[1], "set x=3") // reaches node2 only: dirty, uncommitted
	fmt.Printf("   node1 commitIndex=%d, dirty slot3 on {1,2}\n", nodes[1].CommitIndex())

	fmt.Println("== 4. node3 wins majority; Phase 1 carries slot2 forward ==")
	nodes[3].Campaign()
	mustLeader(nodes[3])
	propose(nodes[3], "set y=3") // lands on slot3 in the majority
	fmt.Printf("   node3 applied=%s\n", applied(nodes[3]))

	fmt.Println("== 5. heal; leader syncs: dirty slot3 on {1,2} safely overwritten ==")
	net.HealAll()
	for _, id := range ids {
		if id != 3 {
			must(nodes[3].SyncFollower(id))
		}
	}
	for _, id := range ids {
		fmt.Printf("   node%d commitIndex=%d applied=%s\n", id, nodes[id].CommitIndex(), applied(nodes[id]))
	}

	fmt.Println("== 6. persistence: replay node1's log via zero-copy mmap ==")
	seg, err := store.Open(filepath.Join(dir, "node1.log"))
	must(err)
	defer seg.Close()
	fmt.Printf("   segment format=v%d, zero-copy entries:\n", seg.Format)
	must(seg.ForEach(func(e store.Entry) error {
		fmt.Printf("     slot=%d term=%d node=%d flags=%d cmd=%q\n",
			e.Slot, e.Term, e.NodeID, e.Flags, e.Command)
		return nil
	}))
	fmt.Println("demo OK")
}

func propose(n *paxos.Node, cmd string) {
	slot, err := n.Propose([]byte(cmd))
	must(err)
	fmt.Printf("   proposed slot=%d cmd=%q\n", slot, cmd)
}

func applied(n *paxos.Node) string {
	s := "["
	for i, e := range n.Applied() {
		if i > 0 {
			s += " "
		}
		if e.Flags&paxos.FlagNoop != 0 {
			s += fmt.Sprintf("%d:noop", e.Slot)
		} else {
			s += fmt.Sprintf("%d:%s", e.Slot, e.Command)
		}
	}
	return s + "]"
}

func mustLeader(n *paxos.Node) {
	if !n.IsLeader() {
		panic(fmt.Sprintf("node%d is not leader", n.ID()))
	}
	fmt.Printf("   node%d is leader\n", n.ID())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
