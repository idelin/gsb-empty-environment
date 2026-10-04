// Command demo runs a 5-node Multi-Paxos cluster through the acceptance
// scenario: leader lease fast path, network partition, old-leader
// fencing, Phase 1 hole repair and safe overwrite of a dirty minority
// log, then final convergence after healing.
package main

import (
	"fmt"
	"os"
	"time"

	"multipaxos/paxos"
)

var failed bool

func check(ok bool, format string, args ...interface{}) {
	if ok {
		fmt.Printf("  [OK]   %s\n", fmt.Sprintf(format, args...))
	} else {
		fmt.Printf("  [FAIL] %s\n", fmt.Sprintf(format, args...))
		failed = true
	}
}

func printLogs(nodes []*paxos.Node) {
	for _, n := range nodes {
		fmt.Printf("  %s commitIndex=%d log:", n.ID(), n.CommitIndex())
		for _, e := range n.Log() {
			mark := " "
			if !e.Committed {
				mark = "*"
			}
			fmt.Printf("  [%d]%s%q", e.Slot, mark, e.Value)
		}
		fmt.Println()
	}
}

func main() {
	net := paxos.NewNetwork()
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(fmt.Sprintf("n%d", i+1), net,
			paxos.WithLeaseDuration(200*time.Millisecond))
	}
	n1, n3 := nodes[0], nodes[2]

	fmt.Println("== 1. n1 wins leadership; lease skips Phase 1 for slots 1-3")
	for _, v := range []string{"set x=1", "set y=2", "set z=3"} {
		if _, err := n1.Propose(v); err != nil {
			fmt.Println("propose failed:", err)
			os.Exit(1)
		}
	}
	prepares, accepts, _ := net.Stats()
	check(prepares == 5, "only one Phase 1 round ran (prepares=%d)", prepares)
	check(accepts == 15, "3 proposals x 5 members accepts (accepts=%d)", accepts)
	printLogs(nodes)

	fmt.Println("== 2. partial cut: n1 writes a dirty entry on minority {n1,n2}")
	net.Cut("n1", "n3")
	net.Cut("n1", "n4")
	net.Cut("n1", "n5")
	_, err := n1.Propose("DIRTY")
	check(err != nil, "minority write rejected: %v", err)
	e, _ := nodes[1].LogEntry(4)
	check(e.Value == "DIRTY" && !e.Committed, "n2 holds uncommitted dirty slot 4")

	fmt.Println("== 3. partition hardens {n1,n2}|{n3,n4,n5}; old leader fenced")
	net.Heal()
	net.Isolate("n1")
	net.Isolate("n2")
	_, err = n1.Propose("stale")
	check(err != nil, "isolated old leader cannot commit: %v", err)

	fmt.Println("== 4. n3 becomes leader in majority, repairs and commits")
	slot, err := n3.Propose("set w=4")
	check(err == nil && slot == 4, "n3 committed slot 4 (slot=%d err=%v)", slot, err)
	printLogs(nodes)

	fmt.Println("== 5. heal; fresh Phase 1 overwrites dirty minority log")
	net.Heal()
	time.Sleep(300 * time.Millisecond)
	if _, err := n3.Propose("set v=5"); err != nil {
		fmt.Println("propose failed:", err)
		os.Exit(1)
	}
	printLogs(nodes)

	want := []string{"set x=1", "set y=2", "set z=3", "set w=4", "set v=5"}
	converged := true
	for _, n := range nodes {
		if n.CommitIndex() != 5 {
			converged = false
		}
		for s := 1; s <= 5; s++ {
			e, ok := n.LogEntry(s)
			if !ok || !e.Committed || e.Value != want[s-1] {
				converged = false
			}
		}
	}
	check(converged, "all 5 nodes converged; dirty value safely overwritten")

	if failed {
		os.Exit(1)
	}
	fmt.Println("demo finished: all checks passed")
}
