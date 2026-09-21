package tasklock_test

import (
	"context"
	"fmt"
	"time"

	"tasklock"
)

// Example demonstrates priority inheritance: while the high-priority
// task waits, the low-priority holder runs at the waiter's priority.
func Example() {
	var s tasklock.Scheduler
	res := s.NewMutex("res")
	low := s.NewTask("low", 1)
	high := s.NewTask("high", 10)

	if err := s.Lock(context.Background(), low, res); err != nil {
		panic(err)
	}
	fmt.Printf("low effective priority before contention: %d\n", low.EffectivePriority())

	acquired := make(chan struct{})
	go func() {
		if err := s.Lock(context.Background(), high, res); err != nil {
			panic(err)
		}
		close(acquired)
	}()
	for res.NumWaiters() == 0 { // wait until high is blocked
		time.Sleep(time.Millisecond)
	}
	fmt.Printf("low effective priority while high waits: %d\n", low.EffectivePriority())

	if err := s.Unlock(low, res); err != nil {
		panic(err)
	}
	<-acquired
	fmt.Printf("low effective priority after release: %d\n", low.EffectivePriority())
	fmt.Printf("res owner now: %s\n", res.Owner().Name())

	// Output:
	// low effective priority before contention: 1
	// low effective priority while high waits: 10
	// low effective priority after release: 1
	// res owner now: high
}
