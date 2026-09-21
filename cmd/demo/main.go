// Command demo shows a round-based rendezvous with participants leaving,
// joining on the next round, and an injected failure propagating to a whole
// round.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"example.com/roundrendezvous/rendezvous"
)

var errInjected = errors.New("injected task failure")

func waitGeneration(b *rendezvous.Barrier, want uint64) {
	for b.Generation() < want {
		time.Sleep(time.Millisecond)
	}
}

func main() {
	const failRound = 4

	var b *rendezvous.Barrier
	var roundNo atomic.Int64
	b = rendezvous.New(3, func(context.Context) error {
		n := roundNo.Add(1)
		if n == failRound {
			fmt.Printf("[round %d] task injected a failure -> aborting the whole round\n", n)
			return errInjected
		}
		fmt.Printf("[round %d] all %d participants arrived, task ran, releasing\n", n, b.Parties())
		return nil
	})

	// worker runs rounds first..last (1-based barrier round numbers).
	worker := func(name string, first, last int) {
		for r := first; r <= last; r++ {
			err := b.Arrive(context.Background())
			if err != nil {
				fmt.Printf("  %s: round %d aborted with %v\n", name, r, err)
			} else {
				fmt.Printf("  %s: finished round %d\n", name, r)
			}
		}
	}

	var wg sync.WaitGroup

	fmt.Println("== rounds 1-2: 3 participants (w1, w2, w3)")
	wg.Add(2)
	go func() { defer wg.Done(); worker("w1", 1, 4) }()
	go func() { defer wg.Done(); worker("w2", 1, 4) }()

	// w3 takes part in round 1. Round 2 cannot complete before w3 acts (only
	// 2 of 3 arrivals are possible until then), so w3 deterministically
	// registers w4's join and its own leave during round 2.
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker("w3", 1, 1)
		b.Add(1)
		fmt.Println("  w3: registered a new participant -> joins from round 3")
		b.Leave()
		fmt.Println("  w3: left the barrier -> round 2 now expects 2 and can release")
	}()

	fmt.Println("== w4 waits for round 3 to join")
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitGeneration(b, 2) // join takes effect on round 3
		worker("w4", 3, 4)
	}()

	wg.Wait()
	fmt.Printf("== done: %d rounds completed, %d participants expected per round now\n",
		b.Generation(), b.Parties())
}
