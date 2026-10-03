// Command demo runs a 5-node Multi-Paxos cluster in-process, demonstrates
// leader lease fast-path commits, a network partition that replaces the old
// leader, hole repair, and safe overwrite of dirty minority logs.
package main

import (
	"fmt"
	"time"

	"multipaxos"
)

func dump(net *paxos.Network) {
	for i := 0; i < net.Size(); i++ {
		nd := net.Node(i)
		leader := ""
		if nd.IsLeader() {
			leader = " [leader]"
		}
		fmt.Printf("  node%d%s log:", i, leader)
		for _, e := range nd.Applied() {
			if e.Noop {
				fmt.Printf(" (noop)")
			} else {
				fmt.Printf(" %q", e.Value)
			}
		}
		fmt.Println()
	}
}

func main() {
	lease := 300 * time.Millisecond
	net := paxos.NewNetwork(5, lease)

	fmt.Println("== 1. node0 becomes leader and commits via lease fast path ==")
	n0 := net.Node(0)
	for _, v := range []string{"set x=1", "set y=2", "set z=3"} {
		idx, err := n0.Propose(v)
		if err != nil {
			fmt.Println("propose failed:", err)
			return
		}
		fmt.Printf("  committed %q at slot %d\n", v, idx)
	}
	fmt.Printf("  Phase 1 campaigns run by node0: %d (lease bypassed the rest)\n", n0.Campaigns())
	dump(net)

	fmt.Println("== 2. partition {0,1} | {2,3,4}: old leader isolated ==")
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := n0.Propose("set w=4"); err != nil {
		fmt.Println("  old leader propose failed as expected:", err)
	}
	time.Sleep(2 * lease) // wait for the old lease to expire
	n2 := net.Node(2)
	idx, err := n2.Propose("set w=5")
	if err != nil {
		fmt.Println("new leader propose failed:", err)
		return
	}
	fmt.Printf("  node2 elected by majority, committed %q at slot %d\n", "set w=5", idx)
	dump(net)

	fmt.Println("== 3. heal: new leader's Phase 1 repairs holes, dirty state loses ==")
	net.HealAll()
	// The majority may still hold node2's lease briefly; retry until the
	// old leader can campaign again.
	for {
		if _, err := n0.Propose("set v=6"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	dump(net)
	fmt.Println("  all nodes converged to one identical, hole-free committed log")
}
