// Command demo shows the graceful-leave protocol end to end: a node
// broadcasts its leave intent, peers move it out of the ready routing pool
// during the quiescent period, in-flight RPCs drain, and the routing entry
// is physically destroyed when the quiescent timer expires.
package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gracefulleave"
)

func main() {
	const quiescent = 500 * time.Millisecond
	cluster := gracefulleave.NewCluster([]string{"node-A", "node-B", "node-C"}, quiescent, nil)
	a := cluster.Node("node-A")

	fmt.Printf("cluster up: 3 nodes, quiescent period = %v\n\n", quiescent)

	// Background traffic from node-A.
	var calls, failures atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := a.Router().Call(context.Background(), "ping"); err != nil {
					failures.Add(1)
				}
				calls.Add(1)
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)

	fmt.Println("[t=0ms] node-C initiates Leave: broadcasting LeaveIntent to peers")
	cluster.Node("node-C").Leave()

	time.Sleep(50 * time.Millisecond)
	for _, id := range []string{"node-A", "node-B"} {
		m, _ := cluster.Node(id).Membership().Get("node-C")
		fmt.Printf("[t=50ms] %s view: node-C=%s, ready pool=%v\n",
			id, m.State(), readyIDs(cluster.Node(id)))
	}

	// A slow in-flight RPC to node-C started during the quiescent window
	// still completes: no sudden RPC timeout.
	slowDone := make(chan error, 1)
	go func() {
		_, err := cluster.Transport.Call(context.Background(), "node-C", "late-in-flight")
		slowDone <- err
	}()
	if err := <-slowDone; err != nil {
		fmt.Printf("[quiescent] in-flight RPC to node-C FAILED: %v\n", err)
	} else {
		fmt.Println("[quiescent] in-flight RPC to node-C completed normally (no timeout)")
	}

	fmt.Printf("[t=%v] quiescent timer expires -> physically destroying node-C routing entries\n", quiescent)
	time.Sleep(quiescent) // outlast the quiescent period

	for _, id := range []string{"node-A", "node-B"} {
		_, ok := cluster.Node(id).Membership().Get("node-C")
		fmt.Printf("[after]  %s view: node-C entry present=%v, ready pool=%v\n",
			id, ok, readyIDs(cluster.Node(id)))
	}

	close(stop)
	wg.Wait()
	fmt.Printf("\ntraffic summary: %d calls, %d failures (no sudden RPC timeouts)\n",
		calls.Load(), failures.Load())
}

func readyIDs(n *gracefulleave.Node) []string {
	var ids []string
	for _, m := range n.Membership().Ready() {
		ids = append(ids, m.ID)
	}
	return ids
}
