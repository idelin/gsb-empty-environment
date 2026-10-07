package main

import (
	"fmt"
	"log"

	"materialsim/sim"
)

func main() {
	s := sim.New()
	events := []sim.Event{
		{ID: "recv-a", Type: sim.EventReceive, Source: "oreA", Quantity: 10},
		{ID: "recv-b", Type: sim.EventReceive, Source: "oreB", Quantity: 6},
		{ID: "blend", Type: sim.EventMerge, Entities: []string{"recv-a/0", "recv-b/0"}},
		{ID: "evap", Type: sim.EventLoss, Entity: "blend/0", Quantity: 2},
		{ID: "cast", Type: sim.EventSplit, Entity: "blend/0", Parts: []int64{9, 5}},
	}
	for _, ev := range events {
		if _, err := s.Apply(ev); err != nil {
			log.Fatal(err)
		}
	}
	// Duplicate delivery of an already-applied event is a no-op.
	dup, err := s.Apply(events[len(events)-1])
	if err != nil || !dup {
		log.Fatalf("expected idempotent duplicate, got dup=%v err=%v", dup, err)
	}

	for _, id := range []string{"cast/0", "cast/1"} {
		line, err := s.Trace(id)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(line)
		if err := s.VerifyTrace(id); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("recorded loss: %d units\n", s.TotalLoss())
	if err := s.VerifyReplay(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("replay check: OK")
}
