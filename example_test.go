package pool_test

import (
	"context"
	"fmt"
	"sync"

	pool "resourcepool"
)

func Example() {
	var mu sync.Mutex
	next := 0
	p := pool.New(1,
		func(ctx context.Context) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			next++
			return next, nil
		},
		func(res any) { fmt.Println("destroyed", res) },
	)

	l, _ := p.Acquire(context.Background())
	fmt.Println("acquired", l.Value())
	l.Release(false)

	l2, _ := p.Acquire(context.Background())
	fmt.Println("reused", l2.Value())
	l2.Release(true) // bad: destroyed, not reused

	p.Close(context.Background(), pool.WaitForReturn)

	// Output:
	// acquired 1
	// reused 1
	// destroyed 1
}
