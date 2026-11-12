package leasefence_test

import (
	"errors"
	"fmt"
	"time"

	"example.org/netsec/q0484/leasefence"
)

// Example shows the host-side protocol: an old job that prepared its commit
// while holding generation 1 is fenced off after the lease expires and the
// resource is re-acquired as generation 2, so its prepared work is rolled back
// instead of mutating the new holder's resource.
func Example() {
	clock := leasefence.NewManualClock(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	fence := leasefence.NewFence(clock, 10*time.Second)
	defer fence.Close()

	old, err := fence.Acquire()
	if err != nil {
		panic(err)
	}
	ticket, err := fence.Begin(old)
	if err != nil {
		panic(err)
	}

	// Prepare blocks for as long as the external transaction requires; the
	// fence lock is never held during this callback.
	prepared := ticket.Token()
	prepare := func(leasefence.Token) error { return nil }

	// Meanwhile the lease deadline passes and the resource is recycled.
	clock.Advance(11 * time.Second)
	fence.ReapExpired()
	current, err := fence.Acquire()
	if err != nil {
		panic(err)
	}

	// The old job reaches its mandatory final validation and loses.
	if err := fence.Commit(ticket, prepare); err != nil {
		fmt.Println("old job rejected:", errors.Is(err, leasefence.ErrFenced))
	}

	// The new holder starts a fresh job and applies cleanly.
	newTicket, err := fence.Begin(current)
	if err != nil {
		panic(err)
	}
	if err := fence.Commit(newTicket, prepare); err != nil {
		panic(err)
	}
	fmt.Printf("new holder applied generation %d\n", newTicket.Token().Generation())
	fmt.Printf("prepared work belonged to generation %d and must roll back\n",
		prepared.Generation())

	// Output:
	// old job rejected: true
	// new holder applied generation 2
	// prepared work belonged to generation 1 and must roll back
}
