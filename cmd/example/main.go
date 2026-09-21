// Command example demonstrates generation fencing: a slow task of the
// old generation is fenced out by a switch, while a new task starts
// immediately without waiting for the old task's cleanup.
package main

import (
	"fmt"
	"time"

	"genfence"
)

func main() {
	m := genfence.NewManager()
	fmt.Printf("current generation: %d\n", m.Generation())

	report := func(o genfence.Outcome) {
		fmt.Printf("  cleanup gen=%d key=%-7s published=%-5v rejected=%-5v err=%v\n",
			o.Gen, o.Key, o.Published, o.Rejected, o.Err)
	}

	// Slow task joins generation 0; the switch will land before it
	// finishes computing.
	m.Start(genfence.Task{
		Key: "report",
		Compute: func() (any, error) {
			time.Sleep(150 * time.Millisecond) // heavy work
			return "q3-report", nil
		},
		Cleanup: report,
	})

	time.Sleep(20 * time.Millisecond)
	fmt.Println("switching generation...")
	m.Switch()
	fmt.Printf("current generation: %d\n", m.Generation())

	// New task starts right away; it does not wait for the old task's
	// cleanup, and it publishes into generation 1.
	m.Start(genfence.Task{
		Key:     "report",
		Compute: func() (any, error) { return "q4-report", nil },
		Cleanup: report,
	})

	m.WaitGeneration(0)
	m.WaitGeneration(1)

	oldCache, _ := m.Snapshot(0)
	curCache, _ := m.Snapshot(1)
	fmt.Printf("generation 0 cache: %v\n", oldCache)
	fmt.Printf("generation 1 cache: %v\n", curCache)
}
