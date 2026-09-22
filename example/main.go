package main

import (
	"fmt"

	"mvreg/sim"
)

func main() {
	s := sim.New[string]([]string{"A", "B", "C"}, 1)
	events := []sim.Event[string]{
		{Time: 1, Kind: sim.Write, Target: "A", Value: "red"},
		{Time: 200, Kind: sim.Write, Target: "B", Value: "blue"},
		{Time: 201, Kind: sim.Send, Target: "A", To: "B"},
		{Time: 203, Kind: sim.Send, Target: "B", To: "A"},
		{Time: 205, Kind: sim.Fail, Target: "C"},
		{Time: 205, Kind: sim.Resolve, Target: "A", Value: "purple"},
		{Time: 206, Kind: sim.Send, Target: "A", To: "C"},
		{Time: 207, Kind: sim.Send, Target: "B", To: "C"},
		{Time: 209, Kind: sim.Recover, Target: "C"},
	}

	for _, entry := range s.Run(events) {
		fmt.Printf("t=%-3d %s: %s\n", entry.Time, entry.Replica, entry.Message)
	}

	fmt.Println("\nfinal values:")
	for _, replica := range []string{"A", "B", "C"} {
		fmt.Printf("%s: %v\n", replica, s.Read(replica))
	}
}
