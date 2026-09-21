// Command demo exercises the bounded resource pool: it caps in-flight
// creation plus borrowed resources at 2, cancels a slow creation without
// leaking anything, and shuts the pool down cleanly.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"resourcepool/pool"
)

type dbConn struct{ id int64 }

func main() {
	var seq int64
	var live int64

	// newPool builds a pool whose factory sleeps d on every create and
	// honors context cancellation.
	newPool := func(d time.Duration) *pool.Pool {
		return pool.New(pool.Config{
			MaxOpen: 2,
			Create: func(ctx context.Context) (any, error) {
				select {
				case <-time.After(d):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				id := atomic.AddInt64(&seq, 1)
				atomic.AddInt64(&live, 1)
				fmt.Printf("  + created connection %d (live=%d)\n", id, atomic.LoadInt64(&live))
				return &dbConn{id: id}, nil
			},
			Close: func(v any) error {
				atomic.AddInt64(&live, -1)
				fmt.Printf("  - closed connection %d (live=%d)\n", v.(*dbConn).id, atomic.LoadInt64(&live))
				return nil
			},
		})
	}

	// Scene 1: 4 acquirers against a limit of 2; healthy resources are reused.
	p := newPool(80 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := p.Acquire(context.Background())
			if err != nil {
				fmt.Printf("worker %d acquire: %v\n", i, err)
				return
			}
			fmt.Printf("worker %d using connection %d\n", i, h.Resource.(*dbConn).id)
			time.Sleep(30 * time.Millisecond)
			_ = h.Release()
		}(i)
	}
	wg.Wait()
	if err := p.Close(); err != nil {
		fmt.Println("close:", err)
	}

	// Scene 2: a slow create is abandoned; the factory is cancelled and no
	// resource is left behind.
	p = newPool(500 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if _, err := p.Acquire(ctx); err != nil {
		fmt.Printf("cancelled slow create: %v\n", err)
	}
	cancel()
	fmt.Printf("live resources after cancelling slow create: %d\n", atomic.LoadInt64(&live))
	if err := p.Close(); err != nil {
		fmt.Println("close:", err)
	}

	if _, err := p.Acquire(context.Background()); errors.Is(err, pool.ErrClosed) {
		fmt.Println("acquire after close: pool closed (expected)")
	}
}
