// Demo of the rendezvous barrier: six rounds with an injected task
// failure, a mid-round leave, and a next-round join.
package main

import (
	"fmt"
	"sync"
	"time"

	"rendezvous"
)

const rounds = 6

func worker(name string, b *rendezvous.Barrier, failAt, leaveAfter int, wg *sync.WaitGroup) {
	defer wg.Done()
	for r := 0; r < rounds; r++ {
		round := r
		start := time.Now()
		err := b.Run(func() error { // task injected per participant
			time.Sleep(50 * time.Millisecond) // injected timing
			if round == failAt {
				return fmt.Errorf("injected failure of %s in round %d", name, round)
			}
			return nil
		})
		fmt.Printf("%s finished round %d (waited %s, err=%v)\n",
			name, round, time.Since(start).Round(time.Millisecond), err)
		if round == leaveAfter {
			b.Leave()
			fmt.Printf("%s left after round %d -> parties=%d\n", name, round, b.Parties())
			return
		}
	}
}

func main() {
	b := rendezvous.New(3)
	var wg sync.WaitGroup

	wg.Add(3)
	go worker("w1", b, -1, -1, &wg)
	go worker("w2", b, 2, -1, &wg) // injected failure in round 2
	go worker("w3", b, -1, 2, &wg) // leaves after round 2

	// w4 joins while round 4 is in progress; it participates from
	// round 5 on.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for b.Round() < 4 {
			time.Sleep(5 * time.Millisecond)
		}
		b.Join()
		first := int(b.Round())
		fmt.Printf("w4 joined, first round=%d -> parties=%d\n", first, b.Parties())
		for r := first; r < rounds; r++ {
			err := b.Run(func() error { time.Sleep(50 * time.Millisecond); return nil })
			fmt.Printf("w4 finished round %d (err=%v)\n", r, err)
		}
	}()

	wg.Wait()
	fmt.Printf("done: rounds=%d parties=%d\n", b.Round(), b.Parties())
}
