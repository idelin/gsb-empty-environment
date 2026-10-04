package main

import "fmt"

// counterThread is a non-atomic shared-counter increment:
// read-modify-write with explicit yield points.
func counterThread(name string) Thread {
	return Thread{Name: name, Ops: []Op{
		ReadOp("x", "t"),
		WriteOp("x", func(l Locals) int { return l["t"] + 1 }),
		ReadOp("done", "d"),
		WriteOp("done", func(l Locals) int { return l["d"] + 1 }),
	}}
}

// lostUpdate: two coroutines increment x; a third waits for both and
// asserts x == 2. A lost update violates the assertion.
func lostUpdate() *Explorer {
	return &Explorer{
		InitMem:  Mem{"x": 0, "done": 0},
		MaxSteps: 32,
		Threads: []Thread{
			counterThread("T1"),
			counterThread("T2"),
			{Name: "T3", Ops: []Op{
				WaitOp("done==2", "", func(m Mem, _ Locals) bool { return m["done"] == 2 }),
				AssertOp("x==2", func(m Mem, _ Locals) bool { return m["x"] == 2 }),
			}},
		},
	}
}

// synchronized: a flag wait orders the writer before the reader, so the
// assertion holds on every schedule.
func synchronized() *Explorer {
	return &Explorer{
		InitMem:  Mem{"x": 0, "ready": 0},
		MaxSteps: 16,
		Threads: []Thread{
			{Name: "T1", Ops: []Op{
				WriteConst("x", 1),
				WriteConst("ready", 1),
			}},
			{Name: "T2", Ops: []Op{
				WaitOp("ready==1", "", func(m Mem, _ Locals) bool { return m["ready"] == 1 }),
				ReadOp("x", "a"),
				AssertOp("a==1", func(_ Mem, l Locals) bool { return l["a"] == 1 }),
			}},
		},
	}
}

func printResult(name string, res Result) {
	fmt.Printf("=== %s ===\n", name)
	fmt.Printf("states=%d transitions=%d truncated=%v\n", res.States, res.Transitions, res.Truncated)
	if res.Violation != nil {
		fmt.Printf("ASSERTION VIOLATED, shortest schedule (%d events):\n", len(res.Violation))
		for _, ev := range res.Violation {
			fmt.Println("  " + ev.String())
		}
	} else {
		fmt.Println("no assertion violation within the step bound")
	}
	if res.Deadlock != nil {
		fmt.Printf("deadlock reachable, shortest schedule (%d events):\n", len(res.Deadlock))
		for _, ev := range res.Deadlock {
			fmt.Println("  " + ev.String())
		}
	}
	fmt.Println()
}

func main() {
	printResult("lost update", lostUpdate().Explore())
	printResult("synchronized", synchronized().Explore())
}
