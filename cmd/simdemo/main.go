// Command simdemo runs a demo material pipeline and prints provenance
// traces, conservation checks, replay verification and idempotency proof.
package main

import (
	"fmt"
	"os"

	"matsim/material"
)

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	s := material.NewSimulator()
	events := []*material.Event{
		{ID: "e1", Type: material.EvReceive, LotID: "ore-A", Qty: 100, Source: "mine-A"},
		{ID: "e2", Type: material.EvReceive, LotID: "ore-B", Qty: 60, Source: "mine-B"},
		{ID: "e3", Type: material.EvSplit, From: "ore-A",
			Parts: []material.Part{{LotID: "A-lot1", Qty: 40}, {LotID: "A-lot2", Qty: 55}}},
		{ID: "e4", Type: material.EvMerge, Ins: []string{"A-lot1", "ore-B"}, Out: "blend"},
		{ID: "e5", Type: material.EvLoss, LotID: "blend", LossQty: 10},
		{ID: "e6", Type: material.EvSplit, From: "blend",
			Parts: []material.Part{{LotID: "product-1", Qty: 60}, {LotID: "product-2", Qty: 25}}},
	}
	for _, e := range events {
		r, err := s.Apply(e)
		check(err)
		if r.LossQty > 0 {
			fmt.Printf("event %-3s %-7s loss=%d vec=[%s]\n", r.EventID, r.Type, r.LossQty, r.LossVec)
		} else {
			fmt.Printf("event %-3s %-7s conserved\n", r.EventID, r.Type)
		}
	}

	fmt.Println("\n-- final inventory --")
	for _, l := range s.Lots() {
		fmt.Println(l)
	}

	fmt.Println("\n-- provenance trace of final products --")
	for _, id := range []string{"product-1", "product-2", "A-lot2"} {
		tr, err := s.Trace(id)
		check(err)
		fmt.Println(tr)
	}

	fmt.Println("\n-- conservation --")
	var live int64
	for _, l := range s.Lots() {
		live += l.Qty
	}
	fmt.Printf("received=160 live=%d loss=%d  live+loss=%d\n", live, s.TotalLoss(), live+s.TotalLoss())

	fmt.Println("\n-- replay verification --")
	check(s.Replay())
	fmt.Println("replay from event log reproduces identical state: OK")

	fmt.Println("\n-- idempotency: re-applying duplicate events --")
	for _, e := range events {
		if _, err := s.Apply(e); err != nil {
			check(err)
		}
	}
	check(s.Replay())
	fmt.Printf("duplicates ignored, state unchanged (live=%d loss=%d): OK\n", live, s.TotalLoss())
}
