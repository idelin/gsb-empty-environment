// Demo of the leasecache package: borrow/evict/release/close lifecycle.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"leasecache"
)

type conn struct{ id int }

func main() {
	var loads, destroys atomic.Int32
	now := time.Now()
	clock := &now
	fail := atomic.Bool{}

	cache := leasecache.New(leasecache.Options[string, *conn]{
		Loader: func(ctx context.Context, key string) (*conn, error) {
			n := loads.Add(1)
			fmt.Printf("[loader ] load %q -> conn#%d\n", key, n)
			return &conn{id: int(n)}, nil
		},
		Destroy: func(c *conn) {
			destroys.Add(1)
			fmt.Printf("[destroy] recycle conn#%d\n", c.id)
		},
		TTL: time.Minute,
		Now: func() time.Time { return *clock },
		Failer: func(op leasecache.Op) error {
			if fail.Load() {
				return errors.New("injected backend failure")
			}
			return nil
		},
	})
	ctx := context.Background()

	fmt.Println("== 1. borrow and release ==")
	a, _ := cache.Get(ctx, "db")
	fmt.Printf("[borrow ] got conn#%d\n", a.Value().id)
	_ = a.Release()

	fmt.Println("== 2. evict while borrowed: borrower keeps object, no reuse ==")
	b, _ := cache.Get(ctx, "db") // same cached conn#1
	cache.Evict("db")
	c, _ := cache.Get(ctx, "db") // must load a NEW conn
	fmt.Printf("[borrow ] evicted borrower holds conn#%d, new caller gets conn#%d\n",
		b.Value().id, c.Value().id)
	_ = b.Release() // conn#1 recycled now
	_ = c.Release()

	fmt.Println("== 3. double release is rejected ==")
	d, _ := cache.Get(ctx, "db")
	_ = d.Release()
	fmt.Printf("[release] second release err: %v\n", d.Release())

	fmt.Println("== 4. injected failure: no pool object leaked ==")
	fail.Store(true)
	_, err := cache.Get(ctx, "cache")
	fmt.Printf("[failure] get err: %v (pool len=%d)\n", err, cache.Len())
	fail.Store(false)

	fmt.Println("== 5. TTL expiry via injected clock ==")
	e, _ := cache.Get(ctx, "ttl")
	*clock = clock.Add(2 * time.Minute)
	f, _ := cache.Get(ctx, "ttl") // lazily expires e's object
	fmt.Printf("[expiry ] old borrower conn#%d, fresh caller conn#%d\n",
		e.Value().id, f.Value().id)
	_ = e.Release()
	_ = f.Release()

	fmt.Println("== 6. close recycles delayed objects ==")
	g, _ := cache.Get(ctx, "held")
	cache.Close() // idle objects recycled immediately
	fmt.Printf("[close  ] destroys so far: %d (borrowed conn#%d still alive)\n",
		destroys.Load(), g.Value().id)
	_ = g.Release() // delayed reclamation
	cache.Wait()
	fmt.Printf("[done   ] loads=%d destroys=%d (equal => no leak)\n",
		loads.Load(), destroys.Load())
}
