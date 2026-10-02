// Demo: a 10-second sliding window over a continuous event stream, with
// out-of-order arrivals, expirations, a single-event retraction, and live
// exact quantile queries.
package main

import (
	"fmt"
	"time"

	"swq"
)

func main() {
	base := time.Unix(0, 0)
	at := func(sec float64) time.Time {
		return base.Add(time.Duration(sec * float64(time.Second)))
	}

	q := swq.New(10 * time.Second)

	// Acceptance scenario: window holds [1, 1, 9, 20].
	q.Add(1, at(0))
	dup := q.Add(1, at(1))
	q.Add(9, at(2))
	q.Add(20, at(3))
	printQ(q, 0.5, "window [1 1 9 20], p50")

	// Retract one of the two 1s -> window [1 9 20].
	q.Remove(dup)
	printQ(q, 0.5, "after retracting one 1, p50")

	// Stream flows in; old events expire as the clock slides.
	fmt.Println("\n-- streaming with expiry (window = 10s) --")
	stream := []struct {
		v   float64
		sec float64
	}{
		{100, 4}, {3, 5}, {7, 6.5}, {42, 8}, {13, 10}, // t=10: events at t<=0 expire
		{77, 11}, {5, 12.5}, // t=12.5: events at t<=2.5 expire
	}
	for _, e := range stream {
		q.Add(e.v, at(e.sec))
		printQ(q, 0.5, fmt.Sprintf("t=%4.1fs add %-5v n=%d, p50", e.sec, e.v, q.Len()))
	}

	// Out-of-order (late) arrival, still inside the window.
	q.Add(55, at(11.5)) // arrives when clock is already at 12.5s
	printQ(q, 0.5, fmt.Sprintf("late add 55@t=11.5, n=%d, p50", q.Len()))
	printQ(q, 0.9, "p90")
	printQ(q, 0.0, "min")
	printQ(q, 1.0, "max")
}

func printQ(q *swq.Quantiler, quantile float64, label string) {
	v, err := q.Quantile(quantile)
	if err != nil {
		fmt.Printf("%-38s -> error: %v\n", label, err)
		return
	}
	fmt.Printf("%-38s -> %v\n", label, v)
}
