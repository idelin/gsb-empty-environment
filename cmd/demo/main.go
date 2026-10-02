// Demo: 3-node cluster running concurrent transfers while node A (id 0)
// initiates a Chandy-Lamport snapshot. The captured local balances plus
// in-flight transfers must sum to the initial total.
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"chandy"
)

const (
	nodes        = 3
	perNode      = 100
	snapshotID   = 1
	trafficDelay = 2 * time.Millisecond
)

func main() {
	rand.Seed(time.Now().UnixNano())
	total := nodes * perNode

	// Fully connected cluster: every ordered pair gets a FIFO channel.
	procs := make([]*chandy.Process, nodes)
	for i := range procs {
		incoming := []int{}
		for j := 0; j < nodes; j++ {
			if j != i {
				incoming = append(incoming, j)
			}
		}
		procs[i] = chandy.NewProcess(i, perNode, incoming)
	}
	stop := make(chan struct{})
	// Links carry 0-5ms of random network latency, so transfers are
	// genuinely in flight while markers travel the same FIFO channels.
	for i := range procs {
		for j := range procs {
			if i != j {
				procs[i].ConnectDelay(procs[j], 5*time.Millisecond, stop)
			}
		}
	}

	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go p.Run(stop, &wg)
	}

	// Concurrent transfer traffic: each node keeps sending 1 unit to a
	// random peer while funds last. Business never pauses.
	var traffic sync.WaitGroup
	for _, p := range procs {
		traffic.Add(1)
		go func(p *chandy.Process) {
			defer traffic.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				to := rand.Intn(nodes)
				if to == p.ID() {
					continue
				}
				p.Transfer(to, 1)
				time.Sleep(time.Duration(rand.Intn(int(trafficDelay))) + time.Millisecond)
			}
		}(p)
	}

	// Let traffic flow, then node A initiates the snapshot.
	time.Sleep(50 * time.Millisecond)
	fmt.Println("=== node A (id 0) initiates Chandy-Lamport snapshot ===")
	procs[0].InitiateSnapshot(snapshotID)

	// Collect every node's snapshot result.
	snaps := make([]chandy.Snapshot, nodes)
	for i, p := range procs {
		select {
		case snaps[i] = <-p.Result():
		case <-time.After(5 * time.Second):
			fmt.Printf("node %d: TIMEOUT waiting for snapshot\n", i)
			return
		}
	}

	// Stop traffic and let in-flight messages drain.
	close(stop)
	traffic.Wait()
	wg.Wait()

	captured := 0
	for _, s := range snaps {
		inFlight := 0
		fmt.Printf("node %d: local balance=%d", s.ProcessID, s.Balance)
		for from, msgs := range s.InFlight {
			for _, m := range msgs {
				inFlight += m.Amount
			}
			fmt.Printf("  in-flight[node %d->node %d]=%d", from, s.ProcessID, len(msgs))
		}
		fmt.Printf("  => captured=%d\n", s.Total())
		captured += s.Total()
	}
	fmt.Printf("captured total (local + in-flight): %d\n", captured)
	fmt.Printf("initial total:                      %d\n", total)
	if captured != total {
		fmt.Println("RESULT: FAIL — money not conserved")
		return
	}
	fmt.Println("RESULT: OK — consistent cut, assets strictly conserved")
}
