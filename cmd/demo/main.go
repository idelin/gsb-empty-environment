// Demo of the fixed-frequency scheduler against the real clock.
// Period is 200ms; round 1 overruns for 450ms, so rounds 2 and 3 are
// skipped and the schedule stays anchored instead of drifting.
package main

import (
	"context"
	"fmt"
	"time"

	"scheduler"
)

func main() {
	anchor := time.Now()
	s := &scheduler.Scheduler{
		Clock:  scheduler.RealClock{},
		Anchor: anchor,
		Period: 200 * time.Millisecond,
		Policy: scheduler.SkipMissed,
		Task: func(ctx context.Context, round int64) error {
			elapsed := time.Since(anchor).Round(time.Millisecond)
			fmt.Printf("round %d started at +%v (scheduled +%v)\n",
				round, elapsed, time.Duration(round)*200*time.Millisecond)
			if round == 1 {
				time.Sleep(450 * time.Millisecond) // overrun two periods
			}
			return nil
		},
		OnError: func(round int64, err error) {
			fmt.Printf("round %d failed: %v\n", round, err)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()
	fmt.Println("scheduler stopped:", s.Run(ctx))
}
