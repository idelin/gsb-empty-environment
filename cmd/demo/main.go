// Command demo reproduces a millisecond-level race for the master
// lease between two nodes and prints the reconstructed audit trail:
// arrival order, arbiter lock transitions, the conflict, the
// rejection, and the causal evidence chain.
package main

import (
	"fmt"
	"sync"
	"time"

	"leaseaudit"
)

func main() {
	log := leaseaudit.NewAuditLog()

	// Stall the first request inside the arbiter's decision critical
	// section just long enough for the second request to arrive --
	// the classic race window in real coordinators.
	release := make(chan struct{})
	var once sync.Once
	arb := leaseaudit.NewArbiter(log, leaseaudit.WithDecideHook(func() {
		once.Do(func() { <-release })
	}))

	clockA, clockB := &leaseaudit.LamportClock{}, &leaseaudit.LamportClock{}

	fmt.Println("=== 两个节点在毫秒级内同时争抢主租约 ===")
	start := make(chan struct{})
	var respA, respB leaseaudit.Response
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		respA = arb.Acquire("node-A", "master", clockA.Tick(), 30*time.Second)
	}()
	go func() {
		defer wg.Done()
		<-start
		respB = arb.Acquire("node-B", "master", clockB.Tick(), 30*time.Second)
	}()
	close(start)

	// Give node B a moment to arrive while A is stalled, then let the
	// arbitration of A complete.
	time.Sleep(5 * time.Millisecond)
	close(release)
	wg.Wait()

	fmt.Printf("node-A 裁决: granted=%v term=%d\n", respA.Granted, respA.Term)
	fmt.Printf("node-B 裁决: granted=%v term=%d (事件 #%d)\n\n", respB.Granted, respB.Term, respB.EventSeq)

	fmt.Println("=== 时间轴回放(逻辑时间戳排序,含仲裁器锁状态) ===")
	fmt.Print(leaseaudit.FormatTimeline(leaseaudit.Replay(log.Events())))

	loser, winner := respA, respB
	loserName := "node-A"
	if respA.Granted {
		loser, winner = respB, respA
		loserName = "node-B"
	}
	fmt.Printf("=== %s 被拒绝的因果关系证据链(胜者事件 #%d) ===\n", loserName, winner.EventSeq)
	fmt.Print(leaseaudit.FormatCausalChain(leaseaudit.CausalChain(log, loser.EventSeq)))
}
