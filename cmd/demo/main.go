// Demo: 5-node Multi-Paxos cluster surviving a network partition, plus a
// saga-style order transaction rolling back with reverse compensation.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"multipaxos/paxos"
	"multipaxos/saga"
)

func paxosDemo() {
	fmt.Println("=== Multi-Paxos: partition, leader replacement, hole repair ===")
	c := paxos.NewCluster(5, time.Hour, nil)
	old, newcomer := c.Nodes[0], c.Nodes[2]

	old.Elect(1)
	old.Propose("set-user", "alice")
	old.Propose("set-config", "v1")
	fmt.Println("term 1: node0 leader, committed slots 1-2")

	c.Net.Partition([]int{0, 1}, []int{2, 3, 4})
	old.Propose("set-secret", "dirty") // minority only -> uncommitted dirty slot 3
	fmt.Println("partition {0,1}|{2,3,4}: slot 3 dirty on minority, uncommitted")

	c.Net.Heal()
	old.Propose("set-order", "ord-1") // slot 4 commits, hole remains at slot 3
	fmt.Println("healed briefly: slot 4 committed, slot 3 is a hole")

	c.Net.Partition([]int{0, 1}, []int{2, 3, 4})
	newcomer.Elect(2) // Phase 1 repairs slot 3 with a no-op, re-chooses slot 4
	newcomer.Propose("set-payment", "pay-1")
	fmt.Println("term 2: node2 leader on majority, repaired hole, committed slot 5")

	c.Net.Heal()
	if _, ok := old.Propose("stale", "write"); !ok {
		fmt.Println("old leader's stale write rejected, it stepped down")
	}
	newcomer.SyncFollowers()

	for _, nd := range c.Nodes {
		fmt.Printf("node %d commitIndex=%d log=%v\n", nd.ID(), nd.CommitIndex(), nd.Committed())
	}
	if err := c.CheckConsistency(); err != nil {
		fmt.Println("INCONSISTENT:", err)
	} else {
		fmt.Println("cluster consistent: no holes, dirty value overwritten")
	}
	fmt.Printf("RPC stats: %+v\n\n", c.Net.Stats)
}

func sagaDemo() {
	fmt.Println("=== Saga: order transaction with reverse compensation ===")
	var log []string
	record := func(format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		log = append(log, msg)
		fmt.Println("  " + msg)
	}

	steps := []saga.Step{
		{Name: "reserve-inventory",
			Action:     func(ctx context.Context) error { record("inventory reserved"); return nil },
			Compensate: func(ctx context.Context) error { record("inventory released"); return nil }},
		{Name: "charge-payment",
			Action:     func(ctx context.Context) error { record("payment charged"); return nil },
			Compensate: func(ctx context.Context) error { record("payment refunded"); return nil }},
		{Name: "ship-order",
			Action:     func(ctx context.Context) error { return errors.New("courier unavailable") },
			Compensate: func(ctx context.Context) error { record("shipment cancelled"); return nil }},
	}

	o := saga.New(saga.Options{})
	err := o.Execute(context.Background(), "order-42", steps)
	fmt.Println("execute returned:", err)
	st, _ := o.Status("order-42")
	fmt.Println("final status:", st)

	// Idempotent: re-executing the rolled-back transaction changes nothing.
	before := len(log)
	o.Execute(context.Background(), "order-42", steps)
	fmt.Printf("re-execution added %d side effects (idempotent)\n", len(log)-before)
}

func main() {
	paxosDemo()
	sagaDemo()
}
