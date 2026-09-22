package main

import (
	"fmt"

	"readindexsim"
)

func waitLeader(sim *readindexsim.Sim) int {
	for tick := 0; tick < 100; tick++ {
		sim.RunFor(1)
		for id := 0; id < 3; id++ {
			if sim.LeaderHint() == id {
				return id
			}
		}
	}
	panic("no leader")
}

func waitResult(sim *readindexsim.Sim, id int) readindexsim.ReadResult {
	for tick := 0; tick < 100; tick++ {
		if result, ok := sim.Result(id); ok {
			return result
		}
		sim.RunFor(1)
	}
	panic("read timeout")
}

func main() {
	sim := readindexsim.NewCluster(false)
	leader := waitLeader(sim)
	fmt.Printf("tick=%d leader=n%d term=%d\n", sim.Tick(), leader, sim.Term(leader))

	sim.Propose(leader, "config", "v1")
	if !sim.WaitCommit(leader, 2, 50) {
		panic("initial write did not commit")
	}
	readID := sim.Read(leader, "config")
	first := waitResult(sim, readID)
	fmt.Printf("normal read=%d value=%q term=%d readIndex=%d acks=%v applied=%d\n",
		first.ReadID, first.Value, first.Evidence.Term, first.Evidence.ReadIndex,
		first.Evidence.Acks, first.Evidence.AppliedIndex)

	sim.Isolate(leader)
	staleRead := sim.Read(leader, "config")
	sim.RunFor(40)
	if _, ok := sim.Result(staleRead); ok {
		panic("isolated old leader obtained evidence")
	}
	fmt.Printf("tick=%d isolated old leader=n%d read=%d result=waiting for quorum\n",
		sim.Tick(), leader, staleRead)

	newLeader := -1
	for tick := 0; tick < 100; tick++ {
		if candidate := sim.LeaderHint(); candidate >= 0 && candidate != leader {
			newLeader = candidate
		}
		if newLeader >= 0 {
			break
		}
		sim.RunFor(1)
	}

	sim.Propose(newLeader, "config", "v2")
	if !sim.WaitCommit(newLeader, 4, 50) {
		panic("new leader write did not commit")
	}
	healthyRead := sim.Read(newLeader, "config")
	second := waitResult(sim, healthyRead)
	fmt.Printf("new leader=n%d read=%d value=%q term=%d readIndex=%d acks=%v applied=%d\n",
		newLeader, second.ReadID, second.Value, second.Evidence.Term,
		second.Evidence.ReadIndex, second.Evidence.Acks, second.Evidence.AppliedIndex)

	sim.RunFor(30)
	if _, ok := sim.Result(staleRead); ok {
		panic("isolated old leader eventually obtained evidence")
	}
	fmt.Printf("tick=%d old isolated read=%d still=waiting; no wall-clock lease used\n",
		sim.Tick(), staleRead)
}
