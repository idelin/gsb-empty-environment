// Command demo runs the simulated cluster through a normal read and a
// partition scenario, printing the read evidence on both paths.
package main

import (
	"fmt"

	"raft"
)

func main() {
	sim := raft.NewSim()
	c := raft.NewCluster(sim, 3)
	c.Trace = func(at int64, format string, args ...any) {
		fmt.Printf("[t=%4d] %s\n", at, fmt.Sprintf(format, args...))
	}

	fmt.Println("== phase 1: election, write, quorum-confirmed read ==")
	sim.RunFor(300)
	leader := c.LeaderID()
	c.Propose(leader, "set x 1")
	sim.RunFor(100)

	c.Read(leader, func(r raft.ReadResult) {
		if !r.OK {
			fmt.Printf("read failed: %s\n", r.Err)
			return
		}
		fmt.Printf("READ OK state=%v\n", r.State)
		fmt.Printf("  evidence: %+v\n", r.Evidence)
		fmt.Printf("  evidence verifies against cluster of 3: %v\n", r.Evidence.Verify(3))
	})
	sim.RunFor(100)

	fmt.Println("\n== phase 2: old leader partitioned away from majority ==")
	c.Partition([]int{0}, []int{1, 2})
	fmt.Printf("[t=%4d] partition: {0} | {1,2}\n", sim.Now())
	c.Read(0, func(r raft.ReadResult) {
		if r.OK {
			fmt.Printf("BUG: isolated leader served read: %+v\n", r.Evidence)
			return
		}
		fmt.Printf("old leader read failed safely: %s\n", r.Err)
	})
	sim.RunFor(400)
	fmt.Printf("[t=%4d] isolated leader produced no evidence (read still pending)\n", sim.Now())

	newLeader := -1
	for _, id := range []int{1, 2} {
		if c.Node(id).Role() == raft.Leader {
			newLeader = id
		}
	}
	c.Propose(newLeader, "set y 2")
	sim.RunFor(200)

	fmt.Println("\n== phase 3: heal; old read fails, new leader reads current state ==")
	c.Heal()
	sim.RunFor(300)
	c.Read(newLeader, func(r raft.ReadResult) {
		if !r.OK {
			fmt.Printf("read failed: %s\n", r.Err)
			return
		}
		fmt.Printf("READ OK state=%v\n", r.State)
		fmt.Printf("  evidence: %+v\n", r.Evidence)
		fmt.Printf("  evidence verifies against cluster of 3: %v\n", r.Evidence.Verify(3))
	})
	sim.RunFor(100)
}
