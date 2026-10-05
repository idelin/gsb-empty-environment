package main

import (
	"fmt"

	"inventorysim/sim"
)

func main() {
	a := sim.NewReplica("A", 40)
	b := sim.NewReplica("B", 30)
	c := sim.NewReplica("C", 20)
	d := sim.NewReplica("D", 10)
	n := sim.NewNetwork(a, b, c, d)

	fmt.Println("== initial ==")
	fmt.Print(n.Report())

	// Partition into {A,B} and {C,D}, then work offline.
	n.Partition("A", "C")
	n.Partition("A", "D")
	n.Partition("B", "C")
	n.Partition("B", "D")

	must(a.Deduct(10))
	must2(n.Transfer("A", "B", 15))
	must2(n.Transfer("B", "C", 20)) // crosses partition: queued
	must(c.Deduct(5))
	must2(n.Transfer("C", "D", 8))
	must2(n.Transfer("D", "A", 5)) // crosses partition: queued

	fmt.Println("== during partition (offline deductions + transfers) ==")
	fmt.Print(n.Report())

	// Reconnect.
	n.Heal("A", "C")
	n.Heal("A", "D")
	n.Heal("B", "C")
	n.Heal("B", "D")

	fmt.Println("== after reconnect (messages delivered, ledgers merged) ==")
	fmt.Print(n.Report())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func must2(_ sim.Transfer, err error) {
	if err != nil {
		panic(err)
	}
}
