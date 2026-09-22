// Command demo shows the group circuit breaker in action with the real clock.
package main

import (
	"errors"
	"fmt"
	"time"

	"groupbreaker"
)

func main() {
	b := groupbreaker.New(groupbreaker.Options{
		FailureThreshold:  2,
		OpenTimeout:       300 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})

	// Group A: two injected failures trip the circuit open.
	for i := 1; i <= 2; i++ {
		err := b.Do("A", func() error { return errors.New("boom") })
		fmt.Printf("A call %d: err=%v -> state=%s\n", i, err, b.State("A"))
	}

	// Open: rejected fast, task never runs.
	err := b.Do("A", func() error { fmt.Println("  (task ran!)"); return nil })
	fmt.Printf("A while open: err=%v (ErrOpen=%v)\n", err, errors.Is(err, groupbreaker.ErrOpen))

	// Group B is independent of A's failure.
	err = b.Do("B", func() error { return nil })
	fmt.Printf("B while A open: err=%v -> state=%s\n", err, b.State("B"))

	// After the open timeout the expiry callback moves A to half-open;
	// a successful probe closes it again.
	time.Sleep(350 * time.Millisecond)
	fmt.Printf("A after timeout: state=%s\n", b.State("A"))
	err = b.Do("A", func() error { return nil })
	fmt.Printf("A probe: err=%v -> state=%s\n", err, b.State("A"))
}
