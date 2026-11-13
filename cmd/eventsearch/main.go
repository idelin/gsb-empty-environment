// Command eventsearch prints the shortest post-revocation resource-call
// interleaving for the safe model and the deliberate vulnerable fixture.
package main

import (
	"fmt"
	"os"

	"example.org/netsec/q0070/eventsearch"
)

func main() {
	opts := eventsearch.Options{
		AllowResurrection:  false,
		OccupiedGlobalSlot: true,
	}

	safe := eventsearch.FindShortest(eventsearch.Safe, opts)
	pending := eventsearch.FindShortest(eventsearch.Pending, opts)

	fmt.Println("safe:")
	fmt.Println(safe.Trace())
	fmt.Println()
	fmt.Println("pending-vulnerable:")
	fmt.Println(pending.Trace())
	fmt.Println()
	fmt.Printf("final state: %+v\n", pending.Final)
	if !safe.Found && pending.Found {
		return
	}
	if safe.Found || !pending.Found {
		fmt.Fprintln(os.Stderr, "unexpected search outcome")
		os.Exit(1)
	}
}
