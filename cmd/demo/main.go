package main

import (
	"fmt"

	mr "majorityregister"
)

func main() {
	sim, err := mr.New(2, mr.Config{Seed: 42, Timeout: 5})
	must(err)

	fmt.Printf("cluster: N=%d, f=%d, majority quorum=%d\n", sim.N(), sim.F(), sim.Quorum())

	writer := sim.NewClient("writer")
	first, err := writer.StartWrite("w-v1", []byte("v1"))
	must(err)
	wait(sim, 50, "first write")
	fmt.Printf("t=%d write v1 -> %s\n", sim.Now(), first.Tag())

	must(sim.Crash(3))
	must(sim.Crash(4))
	second, err := writer.StartWrite("w-v2", []byte("v2"))
	must(err)
	wait(sim, 50, "second write while nodes 3,4 are stopped")
	fmt.Printf("t=%d write v2 with nodes 3,4 stopped -> %s\n", sim.Now(), second.Tag())

	must(sim.Restart(3))
	must(sim.Restart(4))
	must(sim.Crash(0))
	must(sim.Crash(1))

	reader := sim.NewClient("reader")
	read, err := reader.StartRead("read-from-stale-minority")
	must(err)
	wait(sim, 50, "read contacting stale nodes 3,4 and fresh node 2")
	fmt.Printf("t=%d read -> %s=%q; stale nodes 3,4 are repaired\n", sim.Now(), read.Tag(), read.Value())

	must(sim.Restart(0))
	must(sim.Restart(1))
	must(sim.Crash(0))
	must(sim.Crash(1))
	must(sim.Crash(2))
	blocked, err := writer.StartWrite("w-blocked", []byte("v3"))
	must(err)
	if sim.Run(20) {
		panic("write falsely completed without a majority")
	}
	fmt.Printf("t=%d write with only 2/5 live nodes -> %s (not completed)\n", sim.Now(), blocked.Status)

	must(sim.Restart(0))
	must(sim.Restart(1))
	must(sim.Restart(2))
	wait(sim, 50, "write after recovery")
	fmt.Printf("t=%d recovered write -> %s=%q\n", sim.Now(), blocked.Tag(), blocked.Value())
}

func wait(sim *mr.Simulation, ticks uint64, name string) {
	if !sim.Run(ticks) {
		panic(name + " did not finish")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
