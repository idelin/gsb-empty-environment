// Command demo runs the acceptance scenario end to end: a 5-node cluster is
// partitioned, the isolated old leader is replaced by a majority leader, the
// new leader fills uncommitted slots via Phase 1 and safely overwrites the
// minority's dirty log. Finally the offline inspector sweeps the full state.
package main

import (
	"fmt"
	"time"

	"multipaxos"
)

func main() {
	net := multipaxos.NewNetwork()
	nodes := make([]*multipaxos.Node, 5)
	for i := range nodes {
		nodes[i] = multipaxos.NewNode(i+1, net, 500*time.Millisecond)
	}
	oldLeader, newLeader := nodes[0], nodes[2]

	fmt.Println("== 5-node cluster; node 1 campaigns ==")
	oldLeader.Campaign()
	slot, _ := oldLeader.Propose("A")
	fmt.Printf("node 1 committed slot %d = %q on all nodes\n", slot, "A")

	fmt.Println("\n== partition {1,2} | {3,4,5}; old leader writes to minority ==")
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, ok := oldLeader.Propose("dirty"); !ok {
		fmt.Println("old leader's write rejected by majority: slot 2 stays uncommitted (dirty on nodes 1,2)")
	}

	fmt.Println("\n== heal; old leader commits slot 3; nodes 3-5 have a hole at slot 2 ==")
	net.Heal()
	slot, _ = oldLeader.Propose("C")
	fmt.Printf("slot %d = %q committed; node 4 commitIndex=%d (hole below)\n", slot, "C", nodes[3].CommitIndex())

	fmt.Println("\n== partition again; node 3 wins majority campaign and repairs ==")
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	newLeader.Campaign()
	v, committed, _ := nodes[3].Value(2)
	fmt.Printf("node 4 slot 2 = %q committed=%v (Phase 1 filled the hole with a no-op)\n", v, committed)

	fmt.Println("\n== heal; new leader commits slot 4; dirty minority log overwritten ==")
	net.Heal()
	slot, _ = newLeader.Propose("D")
	for _, n := range nodes {
		v2, c2, _ := n.Value(2)
		fmt.Printf("node %d: commitIndex=%d slot2=%q committed=%v\n", n.ID(), n.CommitIndex(), v2, c2)
	}
	fmt.Printf("old leader still leader? %v\n", oldLeader.IsLeader())

	fmt.Println("\n== offline formal inspection ==")
	snaps := make([]multipaxos.Snapshot, len(nodes))
	for i, n := range nodes {
		snaps[i] = n.Snapshot()
	}
	vs := multipaxos.NewInspector().Inspect(snaps)
	if len(vs) == 0 {
		fmt.Println("inspector: all invariants hold, no violations")
	}
	for _, v := range vs {
		fmt.Println("violation:", v)
	}

	fmt.Println("\n== inspector catches an injected dangling reference ==")
	bad := snaps[0].Log[4]
	bad.Refs = []int{2}
	snaps[0].Log[4] = bad
	snaps[0].Committed[2] = false
	delete(snaps[0].Committed, 2)
	for _, v := range multipaxos.NewInspector().Inspect(snaps) {
		fmt.Println("violation:", v)
	}
}
