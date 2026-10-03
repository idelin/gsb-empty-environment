// Demo of the acceptance scenario: 3 workers share a 3000 QPS pool.
// Worker A bursts and exhausts its 1000-token share; batched asynchronous
// heartbeats reallocate idle quota from worker C to A.
package main

import (
	"fmt"
	"time"

	"quota"
)

func main() {
	master := quota.NewMaster(3000, time.Second)
	master.RegisterWorkers("A", "B", "C")

	a := master.NewWorker("A")
	b := master.NewWorker("B")
	c := master.NewWorker("C")
	defer a.Close()
	defer b.Close()
	defer c.Close()

	fmt.Printf("initial grants: A=%d B=%d C=%d (pool=%d)\n\n",
		master.Grant("A"), master.Grant("B"), master.Grant("C"), master.TotalGrants())

	// B serves light background traffic; C stays idle.
	go func() {
		for i := 0; i < 100; i++ {
			b.Allow()
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// A bursts as fast as it can for ~1.5s.
	var allowed int64
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if a.Allow() {
			allowed++
		} else {
			time.Sleep(time.Millisecond) // renewal heartbeat in flight
		}
	}

	fmt.Printf("worker A burst: %d requests allowed (initial share was 1000)\n", allowed)
	fmt.Printf("worker A sent %d heartbeat RPCs for %d tokens (batched renewal)\n\n",
		a.RPCCount(), allowed)

	fmt.Printf("rebalanced grants: A=%d B=%d C=%d (total=%d)\n",
		master.Grant("A"), master.Grant("B"), master.Grant("C"), master.TotalGrants())
	fmt.Printf("idle quota moved from C to A; pool invariant holds: %v\n",
		master.TotalGrants() == 3000)
}
