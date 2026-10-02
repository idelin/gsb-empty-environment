// Command example demonstrates the hierarchical timing wheel: one-shot
// timers, a periodic ticker, cancellation, and discrete clock advancement.
package main

import (
	"fmt"

	"timingwheel"
)

func main() {
	tw := timingwheel.New(8) // base wheel: 8 slots of 1 tick; overflow levels are lazy

	// One-shot timers at various deadlines (some force overflow wheels).
	tw.After(3, func() { fmt.Printf("[tick %3d] one-shot A fired\n", tw.Now()) })
	tw.After(10, func() { fmt.Printf("[tick %3d] one-shot B fired\n", tw.Now()) })
	tw.After(100, func() { fmt.Printf("[tick %3d] one-shot C fired (cascaded from overflow wheel)\n", tw.Now()) })

	// A periodic ticker every 4 ticks.
	ticker := tw.Ticker(4, func() { fmt.Printf("[tick %3d] ticker fired\n", tw.Now()) })

	// A timer that will be cancelled before its deadline: it never fires.
	ghost := tw.After(5, func() { fmt.Printf("[tick %3d] you should never see this\n", tw.Now()) })
	ghost.Cancel()

	// Drive the discrete clock. Cancel the ticker at tick 12.
	for tick := 0; tick < 100; tick++ {
		tw.Advance(1)
		if tw.Now() == 12 {
			ticker.Cancel()
			fmt.Printf("[tick %3d] ticker cancelled\n", tw.Now())
		}
	}

	fmt.Printf("done: now=%d pending=%d (all timers fired or cancelled, memory released)\n",
		tw.Now(), tw.Pending())
}
