// Command sim drives the barrier aggregator through a scripted scenario
// covering out-of-order barrier arrivals, duplicate barriers, partition
// exit, and crash recovery, then runs the event-tag verifier over the
// resulting log.
package main

import (
	"fmt"
	"os"

	"barrieragg/agg"
)

type input struct {
	op        string // join, data, ack, exit, reset
	partition string
	gen       uint64
	value     int64
}

func apply(a *agg.Aggregator, in input) {
	switch in.op {
	case "join":
		a.Join(in.partition)
	case "data":
		a.SubmitData(in.partition, in.gen, in.value)
	case "ack":
		a.BarrierAck(in.partition, in.gen)
	case "exit":
		a.Exit(in.partition)
	case "reset":
		a.IssueReset()
	}
}

// run executes the inputs. If crashAt >= 0, the live aggregator is
// discarded after that many inputs and rebuilt from the event log alone
// (crash recovery) before continuing.
func run(inputs []input, crashAt int) *agg.Aggregator {
	a := agg.New()
	for i, in := range inputs {
		if i == crashAt {
			a = agg.Restore(a.Log())
		}
		apply(a, in)
	}
	return a
}

func scenario() []input {
	return []input{
		{op: "join", partition: "A"}, {op: "join", partition: "B"},
		{op: "join", partition: "C"}, {op: "join", partition: "D"},
		{op: "data", partition: "A", gen: 0, value: 10},
		{op: "data", partition: "B", gen: 0, value: 20},
		{op: "data", partition: "C", gen: 0, value: 5},
		{op: "data", partition: "D", gen: 0, value: 7},
		{op: "reset"}, // barrier for gen 0 issued to A,B,C,D
		{op: "data", partition: "A", gen: 0, value: 3},   // pre-barrier: old gen
		{op: "ack", partition: "C", gen: 0},              // out-of-order arrivals begin
		{op: "data", partition: "C", gen: 1, value: 100}, // post-barrier: buffered to new gen
		{op: "ack", partition: "A", gen: 0},
		{op: "ack", partition: "C", gen: 0}, // duplicate barrier: ignored
		{op: "data", partition: "B", gen: 0, value: 1},
		{op: "exit", partition: "D"},                     // exits before acking: pending set shrinks
		{op: "ack", partition: "D", gen: 0},              // late ack from exited partition: ignored
		{op: "data", partition: "D", gen: 0, value: 999}, // dropped: not a member
		{op: "ack", partition: "B", gen: 0},              // completes the set -> finalize gen 0
		{op: "data", partition: "A", gen: 1, value: 50},
		{op: "reset"},                       // barrier for gen 1
		{op: "ack", partition: "A", gen: 0}, // stale ack from previous gen: ignored
		{op: "ack", partition: "A", gen: 1},
		{op: "ack", partition: "B", gen: 1},
		{op: "ack", partition: "C", gen: 1}, // completes -> finalize gen 1
	}
}

func main() {
	inputs := scenario()

	fmt.Println("=== live run (with mid-scenario crash + recovery) ===")
	a := run(inputs, 14) // crash right after the late-ack input
	for _, ev := range a.Log() {
		fmt.Printf("#%-3d %-10s %-18s p=%-2s gen=%d val=%-4d %s\n",
			ev.Seq, ev.Tag, ev.Kind, dash(ev.Partition), ev.Gen, ev.Value, ev.Note)
	}

	s := a.State()
	fmt.Printf("\nstate: gen=%d old=%d new=%d finals=%v\n", s.CurrentGen, s.OldAcc, s.NewAcc, s.Finals)

	fmt.Println("\n=== event-tag verification ===")
	violations := agg.Verify(a.Log())
	if len(violations) == 0 {
		fmt.Println("OK: all invariants hold (exactly-once, completeness, no-duplicates, exit-safety, value-safety, carry-safety, timeliness)")
	} else {
		for _, v := range violations {
			fmt.Printf("VIOLATION @%d: %s\n", v.Seq, v.Msg)
		}
		os.Exit(1)
	}

	fmt.Println("\n=== recovery equivalence ===")
	ref := run(inputs, -1)
	bad := 0
	for crashAt := 0; crashAt <= len(inputs); crashAt++ {
		got := run(inputs, crashAt)
		if got.State().Hash() != ref.State().Hash() {
			fmt.Printf("MISMATCH: crash at input %d diverges\n", crashAt)
			bad++
		}
	}
	if bad == 0 {
		fmt.Printf("OK: recovery from the log at every one of %d crash points reproduces the live state\n", len(inputs)+1)
	} else {
		os.Exit(1)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
