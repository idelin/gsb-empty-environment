// Command expsim demonstrates the sequential experiment simulator:
// accumulate to first boundary touch, checkpoint/recover, then apply
// a late revision and emit compensation suggestions.
package main

import (
	"fmt"

	"expsim/expsim"
)

func main() {
	b := expsim.Boundaries{
		Upper: []int64{10, 10, 10, 10, 10},
		Lower: []int64{-10, -10, -10, -10, -10},
	}
	events := []expsim.Event{
		expsim.ObserveEvent(expsim.Observation{ID: "o1", Time: 1, Seq: 1, Value: 3}),
		expsim.ObserveEvent(expsim.Observation{ID: "o2", Time: 2, Seq: 1, Value: 4}),
		expsim.CheckpointEvent(),
		expsim.ObserveEvent(expsim.Observation{ID: "o3", Time: 3, Seq: 1, Value: 5}),
		expsim.ActionEvent(expsim.Action{Step: 3, Kind: "ship"}),
		expsim.ObserveEvent(expsim.Observation{ID: "o4", Time: 4, Seq: 1, Value: 7}),
		expsim.ReviseEvent(expsim.Revision{ObsID: "o2", Value: 9, Note: "sensor recalibrated"}),
	}

	s, err := expsim.Replay(b, events)
	if err != nil {
		panic(err)
	}

	ev := s.Engine().Evidence()
	fmt.Printf("live run (fact v0): stopped at %v\n", ev)
	for _, c := range s.Conclusions() {
		fmt.Printf("recomputed (fact v%d): stop step=%d decision=%s\n", c.FactVersion, c.StopStep, c.Decision)
	}
	fmt.Printf("stop shifts: %v\n", s.Shifts())
	for _, c := range s.Compensations() {
		fmt.Printf("compensation for %s: %s\n", c.ActionID, c.Suggestion)
	}
	fmt.Printf("ledger (untouched): %+v\n", s.Ledger().Actions())
	if err := expsim.VerifyRecovery(b, events); err != nil {
		panic(err)
	}
	fmt.Println("checkpoint recovery verified by event replay")
}
