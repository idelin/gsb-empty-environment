// Command demo runs the acceptance scenario of the event-driven exact
// rate-limiting bucket: rule = at most 3 requests per 10 seconds.
package main

import (
	"fmt"
	"time"

	limit "eventlimiter"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return epoch.Add(time.Duration(sec) * time.Second) }

func main() {
	l := limit.New(3, 10*time.Second)
	fmt.Println("rule: at most 3 requests per 10s (event-time sliding window)")

	submit := func(id string, sec int) {
		v, corr := l.Submit(id, at(sec))
		fmt.Printf("arrive %-8s event t=%-2ds -> %s\n", id, sec, v)
		for _, c := range corr {
			fmt.Printf("    !! %s\n", c)
		}
	}

	submit("req-1", 1)
	submit("req-2", 2)
	submit("req-8", 8)

	fmt.Println("-- late request with event time t=0 arrives --")
	submit("req-0", 0)

	fmt.Println("final history (ordered by event time):")
	for _, e := range l.History() {
		fmt.Printf("    %-8s t=%-2ds %s\n", e.ID, int(e.Time.Sub(epoch)/time.Second), e.Verdict)
	}
}
