package pool_test

import (
	"context"
	"fmt"
	"sync/atomic"

	"resourcepool/pool"
)

type conn struct{ id int64 }

// ExamplePool shows a pool limiting an expensive resource (connections) to 2,
// reusing healthy ones and destroying a bad one.
func ExamplePool() {
	var seq int64
	var live int64
	p := pool.New(pool.Config{
		MaxOpen: 2,
		Create: func(ctx context.Context) (any, error) {
			id := atomic.AddInt64(&seq, 1)
			atomic.AddInt64(&live, 1)
			return &conn{id: id}, nil
		},
		Close: func(v any) error {
			atomic.AddInt64(&live, -1)
			return nil
		},
	})

	use := func(bad bool) {
		h, err := p.Acquire(context.Background())
		if err != nil {
			fmt.Println("acquire:", err)
			return
		}
		fmt.Println("using connection", h.Resource.(*conn).id)
		if bad {
			h.MarkBad()
		}
		if err := h.Release(); err != nil {
			fmt.Println("release:", err)
		}
	}

	use(false) // creates connection 1
	use(false) // reuses connection 1
	use(true)  // reuses 1, then destroys it as bad
	use(false) // creates connection 2
	fmt.Println("live before close:", atomic.LoadInt64(&live))
	if err := p.Close(); err != nil {
		fmt.Println("close:", err)
	}
	fmt.Println("live after close:", atomic.LoadInt64(&live))

	// Output:
	// using connection 1
	// using connection 1
	// using connection 1
	// using connection 2
	// live before close: 1
	// live after close: 0
}
