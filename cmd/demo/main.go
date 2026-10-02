// Demo: 5-node Multi-Paxos cluster surviving a network partition, followed by
// a saga with reverse compensation on failure.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"paxosaga/paxos"
	"paxosaga/saga"
)

func paxosDemo() {
	fmt.Println("=== Multi-Paxos: partition, leader failover, gap repair ===")
	net := paxos.NewNetwork()
	nodes := map[string]*paxos.Node{}
	for _, id := range []string{"A", "B", "C", "D", "E"} {
		nodes[id] = paxos.NewNode(net, id, 2*time.Second)
	}
	set := func(k, v string) paxos.Op { return paxos.Op{Kind: paxos.OpSet, Key: k, Value: v} }

	a, c := nodes["A"], nodes["C"]
	slot, _ := a.Propose(set("x", "1"))
	fmt.Printf("[pre-partition]  A(leader) committed slot %d: x=1 (Phase1 rounds=%d)\n", slot, a.PrepareRounds)
	slot, _ = a.Propose(set("y", "2"))
	fmt.Printf("[pre-partition]  A committed slot %d via lease fast-path (Phase1 rounds still %d)\n", slot, a.PrepareRounds)

	net.Partition([]string{"A", "B"}, []string{"C", "D", "E"})
	fmt.Println("[partition]      cluster split: {A,B} | {C,D,E}")

	err := func() error { _, e := a.Propose(set("z", "dirty")); return e }()
	fmt.Printf("[minority]       old leader A proposes z=dirty -> %v (dirty entry on A,B only)\n", err)

	if err := c.BecomeLeader(); err != nil {
		panic(err)
	}
	fmt.Println("[majority]       C won Phase 1, repaired slots 1-2 from promises, became leader")
	slot, _ = c.Propose(set("z", "new"))
	fmt.Printf("[majority]       C committed slot %d: z=new (lease fast-path)\n", slot)

	net.Heal()
	if err := c.SyncAll(); err != nil {
		panic(err)
	}
	fmt.Println("[healed]         C re-replicated slots 1-3; dirty minority log overwritten")
	for _, id := range []string{"A", "B", "C", "D", "E"} {
		n := nodes[id]
		e, _ := n.LogEntry(3)
		fmt.Printf("                 node %s: commitIndex=%d slot3=%q sm=%v\n", id, n.CommitIndex(), e.Op.Value, n.StateMachine())
	}
	if _, err := a.Propose(set("w", "stale")); errors.Is(err, paxos.ErrNoMajority) {
		fmt.Println("[safety]         stale leader A rejected by higher ballot and stepped down")
	}
	fmt.Println()
}

func sagaDemo() {
	fmt.Println("=== Saga: reverse compensation & rollback orchestration ===")
	store := saga.NewStore()
	orch := saga.NewOrchestrator(store, saga.WithRetries(3), saga.WithBackoff(10*time.Millisecond))

	balance := map[string]int{"alice": 100, "bob": 0}
	debit := func(ctx context.Context) error {
		balance["alice"] -= 100
		fmt.Println("  [action]     debit alice 100   ->", balance)
		return nil
	}
	refund := func(ctx context.Context) error {
		balance["alice"] += 100
		fmt.Println("  [compensate] refund alice 100 ->", balance)
		return nil
	}
	attempts := 0
	credit := func(ctx context.Context) error {
		attempts++
		fmt.Printf("  [action]     credit bob 100 (attempt %d) -> FAILED\n", attempts)
		return errors.New("bob's bank is unreachable")
	}

	steps := []saga.Step{
		{Name: "debit-alice", Action: debit, Compensate: refund},
		{Name: "credit-bob", Action: credit, Compensate: func(context.Context) error { return nil }},
	}
	err := orch.Run(context.Background(), "transfer-1", steps)
	fmt.Println("  result:", err)
	fmt.Printf("  final state: status=%s history=%v balances=%v\n",
		store.Status("transfer-1"), store.History("transfer-1"), balance)

	// Idempotent re-run of a compensated saga is a no-op.
	if err := orch.Run(context.Background(), "transfer-1", steps); err != nil {
		panic(err)
	}
	fmt.Printf("  idempotent re-run: balances unchanged %v\n", balance)
}

func main() {
	paxosDemo()
	sagaDemo()
}
