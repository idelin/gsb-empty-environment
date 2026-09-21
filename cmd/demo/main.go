// Command demo exercises the pool: slow creation, cancellation reclaim,
// bad-resource destruction, and graceful close.
package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pool "resourcepool"
)

func main() {
	var created, destroyed atomic.Int32
	gate := make(chan struct{})

	p := pool.New(3,
		func(ctx context.Context) (any, error) {
			<-gate // creation is slow; the demo controls when it finishes
			id := created.Add(1)
			fmt.Printf("[factory] created resource #%d\n", id)
			return id, nil
		},
		func(res any) {
			fmt.Printf("[destroy] resource #%v\n", res)
			destroyed.Add(1)
		},
	)

	// Two acquisitions are cancelled while their creations are in flight.
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := p.Acquire(ctx)
			fmt.Printf("[acquire %d] cancelled: %v\n", i, err)
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	// Late creations finish; their resources are reclaimed, not leaked.
	close(gate)
	time.Sleep(50 * time.Millisecond)

	// Normal borrow/return, then a bad return that destroys the resource.
	l, _ := p.Acquire(context.Background())
	fmt.Printf("[borrow] got resource #%v\n", l.Value())
	l.Release(true) // bad
	fmt.Println("[return] returned as bad")

	// Graceful close waits for the outstanding lease to come back.
	l2, _ := p.Acquire(context.Background())
	closed := make(chan struct{})
	go func() {
		p.Close(context.Background(), pool.WaitForReturn)
		close(closed)
	}()
	time.Sleep(50 * time.Millisecond)
	fmt.Println("[close] waiting for borrowed resource...")
	l2.Release(false)
	<-closed

	fmt.Printf("[done] created=%d destroyed=%d (no leaked resources)\n",
		created.Load(), destroyed.Load())
}
