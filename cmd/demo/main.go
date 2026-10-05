// Demo of the observed-remove multi-value map using local synthetic
// events: three replicas assign and observe-remove the same key, exchange
// ops out of order with duplicates, export/restore a checkpoint, and
// replay stale messages.
package main

import (
	"fmt"
	"sort"

	"ormap"
)

func show(r *ormap.Replica, key string) {
	live := r.Live(key)
	var dots []string
	for dot, value := range live {
		dots = append(dots, fmt.Sprintf("%s/%d=%q", dot.Replica, dot.Counter, value))
	}
	sort.Strings(dots)
	display, conflict := r.Display(key)
	fmt.Printf("  replica %-6s live=%v display=%q conflict=%v\n", r.ID(), dots, display, conflict)
}

func main() {
	key := "nickname"
	alice := ormap.NewReplica("alice")
	bob := ormap.NewReplica("bob")
	carol := ormap.NewReplica("carol")

	fmt.Println("== concurrent assignments ==")
	opA1 := alice.Set(key, "ally")
	opB1 := bob.Set(key, "bobby")
	// Exchange in different orders, with a duplicate delivery.
	_ = bob.Apply(opA1)
	_ = alice.Apply(opB1)
	_ = alice.Apply(opB1) // duplicate: harmless
	show(alice, key)
	show(bob, key)

	fmt.Println("== bob removes what he has observed; alice concurrently re-assigns ==")
	opRm := bob.Remove(key)
	opA2 := alice.Set(key, "al")
	show(bob, key) // empty: both observed dots removed
	show(alice, key)

	fmt.Println("== ops cross in the network ==")
	_ = alice.Apply(opRm)
	_ = bob.Apply(opA2)
	show(alice, key)
	show(bob, key) // alice's concurrent, unobserved assignment survived

	fmt.Println("== carol joins via checkpoint of alice ==")
	data, err := alice.State().Export()
	if err != nil {
		panic(err)
	}
	restored, err := ormap.Import(data)
	if err != nil {
		panic(err)
	}
	if err := carol.MergeState(restored); err != nil {
		panic(err)
	}
	show(carol, key)

	fmt.Println("== stale messages replayed after restore: no resurrection ==")
	for _, op := range []*ormap.Op{opA1, opB1, opA1} {
		if err := carol.Apply(op); err != nil {
			panic(err)
		}
	}
	show(carol, key)

	fmt.Println("== same dot with different content is rejected ==")
	bad := ormap.NewSetOp("alice", 2, key, "forged")
	if err := carol.Apply(bad); err != nil {
		fmt.Println("  rejected:", err)
	}
}
