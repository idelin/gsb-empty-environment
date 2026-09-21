// Command demo runs the breaker against a flaky backend with the real clock.
package main

import (
	"errors"
	"fmt"
	"time"

	"breaker"
)

func main() {
	m := breaker.NewManager(breaker.Options{
		FailureThreshold:  2,
		OpenDuration:      300 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})

	calls := 0
	backend := func() error {
		calls++
		if calls <= 4 { // backend recovers on the 5th actual call
			return errors.New("backend unavailable")
		}
		return nil
	}

	for i := 1; i <= 14; i++ {
		err := m.Execute("payments", backend)
		fmt.Printf("t=%4dms  attempt %02d  state=%-9s err=%v\n",
			(i-1)*100, i, m.State("payments"), err)
		time.Sleep(100 * time.Millisecond)
	}
}
