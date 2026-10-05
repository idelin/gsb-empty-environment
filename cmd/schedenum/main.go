// Command schedenum runs the cooperative schedule enumerator against two
// built-in buggy programs and prints the shortest violating schedule.
package main

import (
	"flag"
	"fmt"
	"os"

	"schedenum/sched"
)

func lostWakeup() *sched.Machine {
	return &sched.Machine{
		MemSize: 1, // mem[0] = flag
		Regs:    1,
		Progs: [][]sched.Instr{
			{ // consumer: check flag, wait if unset, then re-check
				{Kind: sched.Load, Dst: 0, Addr: 0},
				{Kind: sched.JmpIf, Cond: sched.Bin{Op: sched.Ne, L: sched.Reg(0), R: sched.Const(0)}, Target: 3},
				{Kind: sched.Wait, Cv: 0},
				{Kind: sched.Load, Dst: 0, Addr: 0},
				{Kind: sched.Assert, Val: sched.Bin{Op: sched.Eq, L: sched.Reg(0), R: sched.Const(1)}, Note: "flag == 1 after wakeup"},
			},
			{ // producer: set flag, notify waiters
				{Kind: sched.Store, Addr: 0, Val: sched.Const(1)},
				{Kind: sched.Notify, Cv: 0},
			},
		},
	}
}

func counterRace() *sched.Machine {
	worker := func(doneAddr int) []sched.Instr {
		return []sched.Instr{
			{Kind: sched.Load, Dst: 0, Addr: 0},
			{Kind: sched.Store, Addr: 0, Val: sched.Bin{Op: sched.Add, L: sched.Reg(0), R: sched.Const(1)}},
			{Kind: sched.Store, Addr: doneAddr, Val: sched.Const(1)},
		}
	}
	return &sched.Machine{
		MemSize: 3, // mem[0] = counter, mem[1..2] = done flags
		Regs:    1,
		Progs: [][]sched.Instr{
			worker(1),
			worker(2),
			{ // monitor: wait for both workers, assert counter == 2
				{Kind: sched.Load, Dst: 0, Addr: 1},
				{Kind: sched.JmpIf, Cond: sched.Bin{Op: sched.Eq, L: sched.Reg(0), R: sched.Const(0)}, Target: 0},
				{Kind: sched.Load, Dst: 0, Addr: 2},
				{Kind: sched.JmpIf, Cond: sched.Bin{Op: sched.Eq, L: sched.Reg(0), R: sched.Const(0)}, Target: 0},
				{Kind: sched.Load, Dst: 0, Addr: 0},
				{Kind: sched.Assert, Val: sched.Bin{Op: sched.Eq, L: sched.Reg(0), R: sched.Const(2)}, Note: "counter == 2"},
			},
		},
	}
}

func run(name string, m *sched.Machine, maxSteps int) bool {
	res, err := sched.Explore(m, sched.Options{MaxSteps: maxSteps})
	if err != nil {
		fmt.Printf("== %s: error: %v\n", name, err)
		return false
	}
	fmt.Printf("== %s: explored %d states, merged %d duplicates\n", name, res.Explored, res.Merged)
	if res.Violation == nil {
		fmt.Printf("   no violation within %d steps (truncated=%t)\n\n", maxSteps, res.Truncated)
		return true
	}
	fmt.Printf("   VIOLATION (%s): %s\n", res.Violation.Kind, res.Violation.Message)
	fmt.Printf("   shortest schedule (%d events):\n", len(res.Violation.Trace))
	for i, ev := range res.Violation.Trace {
		fmt.Printf("   %3d. T%d  %s\n", i+1, ev.Thread, ev.Text)
	}
	fmt.Println()
	return false
}

func main() {
	maxSteps := flag.Int("steps", 64, "maximum scheduling steps per trace")
	flag.Parse()

	ok := true
	ok = run("lost wakeup (deadlock)", lostWakeup(), *maxSteps) && ok
	ok = run("non-atomic counter (assertion)", counterRace(), *maxSteps) && ok
	if !ok {
		os.Exit(1)
	}
}
