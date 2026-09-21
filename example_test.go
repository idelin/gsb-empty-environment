package breaker_test

import (
	"errors"
	"fmt"
	"time"

	"breaker"
)

// Deterministic walk through closed -> open -> half-open -> closed using an
// injected fake clock and scheduler.
func ExampleManager() {
	ft := newFakeTime(time.Unix(1000, 0))
	m := breaker.NewManager(breaker.Options{
		FailureThreshold:  2,
		OpenDuration:      10 * time.Second,
		HalfOpenMaxProbes: 1,
		Clock:             ft,
		Scheduler:         ft,
	})
	fail := func() error { return errors.New("backend down") }
	ok := func() error { return nil }

	m.Execute("payments", fail) // failure 1
	m.Execute("payments", fail) // failure 2 -> open
	fmt.Println("after failures:", m.State("payments"))

	err := m.Execute("payments", ok)
	fmt.Println("open call:", err) // fast fail, task not run

	ft.Advance(10 * time.Second) // expiry callback fires
	fmt.Println("after expiry:", m.State("payments"))

	m.Execute("payments", ok) // probe succeeds
	fmt.Println("after probe:", m.State("payments"))

	// Output:
	// after failures: open
	// open call: breaker: circuit is open
	// after expiry: half-open
	// after probe: closed
}
