// Demo: period 200ms, each run takes 500ms (2.5 periods). With SkipMissed
// the schedule stays anchored to the base time (rounds 0, 3, 6, ...) instead
// of drifting to "end of run + period".
package main

import (
	"context"
	"fmt"
	"time"

	scheduler "fixedsched"
)

func main() {
	start := time.Now()
	base := start.Add(300 * time.Millisecond) // first round slightly ahead
	stamp := func() string { return fmt.Sprintf("t+%4.1fs", time.Since(start).Seconds()) }

	task := func(ctx context.Context, round int64) error {
		fmt.Printf("%s  round %d start\n", stamp(), round)
		time.Sleep(500 * time.Millisecond) // overrun: 2.5x the period
		fmt.Printf("%s  round %d done\n", stamp(), round)
		return nil
	}

	s := scheduler.New(scheduler.RealClock{}, task, scheduler.Options{
		Base:   base,
		Period: 200 * time.Millisecond,
		Policy: scheduler.SkipMissed,
		OnError: func(round int64, err error) {
			fmt.Printf("%s  round %d error: %v\n", stamp(), round, err)
		},
	})
	s.Start()
	time.Sleep(2300 * time.Millisecond)
	s.Stop() // cancels the loop and disposes of the pending timer
	fmt.Printf("%s  stopped, last executed round = %d\n", stamp(), s.LastRound())
}
