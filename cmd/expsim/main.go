package main

import (
	"fmt"

	"expsim/sim"
)

// Demo: sequential experiment with bounds +/-10, a late revision that
// erases the first stop, and the resulting compensation suggestion.
func main() {
	s := sim.NewSimulator(-10, 10)
	events := []sim.Event{
		{Kind: sim.EventObserve, Key: sim.ObsKey{Time: 1, Seq: 1}, Value: 6},
		{Kind: sim.EventObserve, Key: sim.ObsKey{Time: 2, Seq: 1}, Value: 5},
		{Kind: sim.EventRevise, Key: sim.ObsKey{Time: 2, Seq: 1}, Value: 1},
		{Kind: sim.EventObserve, Key: sim.ObsKey{Time: 3, Seq: 1}, Value: 8},
	}
	for _, ev := range events {
		for _, entry := range s.Apply(ev) {
			fmt.Printf("action v%d %-11s sum=%d steps=%d reason=%s\n",
				entry.Version, entry.Kind, entry.Conclusion.Sum, entry.Conclusion.Steps, entry.Reason)
		}
	}
	final := s.Conclusions[len(s.Conclusions)-1]
	fmt.Printf("final conclusion: stopped=%v edge=%s sum=%d steps=%d evidence=%+v\n",
		final.Stopped, final.Boundary, final.Sum, final.Steps, final.Evidence)
}
