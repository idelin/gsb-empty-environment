// Demo of the leasecache package: borrowable cached objects with
// reference-counted leases, eviction, failure injection and delayed
// reclamation on Close. Run with: go run ./cmd/demo
package main

import (
	"errors"
	"fmt"
	"time"

	"leasecache"
)

// conn is a pretend pooled resource (e.g. a database connection).
type conn struct {
	id int
}

func main() {
	nextID := 0
	flakyDown := true // injected failure: first "flaky" load fails

	// Injectable data source.
	loader := func(key string) (*conn, error) {
		if key == "flaky" && flakyDown {
			flakyDown = false
			return nil, errors.New("dial timeout")
		}
		nextID++
		return &conn{id: nextID}, nil
	}
	// Injectable teardown, used to verify resource release.
	recycler := func(c *conn) { fmt.Printf("  recycle conn #%d\n", c.id) }

	// Injectable clock: TTL entries expire against this fake time.
	now := time.Now()
	cache := leasecache.New[string, *conn](loader, recycler, leasecache.Config{
		TTL:   time.Minute,
		Clock: func() time.Time { return now },
	})

	fmt.Println("1. borrow the same key twice -> one shared object")
	a, _ := cache.Acquire("db")
	b, _ := cache.Acquire("db")
	fmt.Printf("  a=conn #%d, b=conn #%d (shared: %v)\n", a.Value().id, b.Value().id, a.Value() == b.Value())

	fmt.Println("2. evict while borrowed -> borrower unaffected, object not reused")
	cache.Evict("db")
	c, _ := cache.Acquire("db")
	fmt.Printf("  new caller gets conn #%d (evicted conn #%d not reused)\n", c.Value().id, a.Value().id)
	fmt.Println("  releasing a and b recycles the old object only after the last lease:")
	a.Release()
	b.Release()
	c.Release()

	fmt.Println("3. double release is rejected")
	d, _ := cache.Acquire("db")
	cache.Evict("db")
	d.Release()
	fmt.Printf("  second release error: %v\n", d.Release())

	fmt.Println("4. injected load failure leaks nothing")
	if _, err := cache.Acquire("flaky"); err != nil {
		fmt.Printf("  acquire failed: %v (stats: %+v, cached entries: %d)\n", err, cache.Stats(), cache.Len())
	}
	e, _ := cache.Acquire("flaky") // retry succeeds
	fmt.Printf("  retry got conn #%d\n", e.Value().id)
	e.Release()

	fmt.Println("5. TTL expiry via injected clock")
	g, _ := cache.Acquire("session")
	g.Release() // idle in cache
	now = now.Add(2 * time.Minute)
	h, _ := cache.Acquire("session")
	fmt.Printf("  clock advanced past TTL -> stale conn #%d recycled, fresh conn #%d served\n", g.Value().id, h.Value().id)

	fmt.Println("6. close: idle objects recycled now, borrowed one on release")
	if err := cache.Close(); err != nil {
		fmt.Println("  close:", err)
	}
	fmt.Println("  releasing the still-borrowed lease (delayed reclamation):")
	h.Release()

	fmt.Printf("done. stats: %+v (loaded == recycled => no leaks)\n", cache.Stats())
}
