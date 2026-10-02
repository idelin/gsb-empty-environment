// Command demo shows the timing wheel in action: one-shot timers, a
// periodic ticker, and cancellation, all driven by a discrete clock.
package main

import (
	"fmt"

	"timingwheel"
)

func main() {
	tw := timingwheel.New(0)

	tw.AfterFunc(3, func(now int64) {
		fmt.Printf("[tick %2d] one-shot A fired   (deadline 3)\n", now)
	})
	tw.AfterFunc(9, func(now int64) {
		fmt.Printf("[tick %2d] one-shot B fired   (deadline 9)\n", now)
	})
	cancelled := tw.AfterFunc(5, func(now int64) {
		fmt.Printf("[tick %2d] never printed: this timer is cancelled\n", now)
	})
	ticker := tw.TickFunc(2, func(now int64) {
		fmt.Printf("[tick %2d] ticker fired       (period 2)\n", now)
	})

	cancelled.Cancel()
	fmt.Println("scheduled: A@3, B@9, ticker every 2, cancelled@5 (already cancelled)")

	for tick := int64(1); tick <= 12; tick++ {
		tw.Advance()
		if tick == 8 {
			ticker.Cancel()
			fmt.Printf("[tick %2d] ticker cancelled\n", tick)
		}
	}
	fmt.Printf("done: now=%d active=%d\n", tw.Now(), tw.Active())
}
