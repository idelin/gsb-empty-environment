package leasecache_test

import (
	"errors"
	"fmt"
	"time"

	"leasecache"
)

// Example shows the full lifecycle: shared borrows, eviction that keeps
// borrowers alive, a rejected double release, an injected load failure,
// TTL expiry on a fake clock, and delayed reclamation at Close.
func Example() {
	nextID := 0
	fail := true
	loader := func(key string) (*string, error) {
		if fail {
			fail = false
			return nil, errors.New("boom")
		}
		nextID++
		s := fmt.Sprintf("obj-%d", nextID)
		return &s, nil
	}
	recycler := func(s *string) { fmt.Println("recycled", *s) }

	now := time.Unix(0, 0)
	c := leasecache.New[string, *string](loader, recycler, leasecache.Config{
		TTL:   time.Minute,
		Clock: func() time.Time { return now },
	})

	if _, err := c.Acquire("k"); err != nil {
		fmt.Println("load failed:", err)
	}
	l1, _ := c.Acquire("k")
	l2, _ := c.Acquire("k")
	fmt.Println("shared:", *l1.Value(), l1.Value() == l2.Value())

	c.Evict("k")
	l3, _ := c.Acquire("k")
	fmt.Println("after evict, new caller gets:", *l3.Value())
	l1.Release()
	l2.Release() // last lease on the evicted object: recycled now

	fmt.Println("double release:", l2.Release())

	now = now.Add(2 * time.Minute) // expire l3's entry
	l4, _ := c.Acquire("k")
	fmt.Println("after TTL, fresh object:", *l4.Value())
	l3.Release()

	c.Close() // recycles the idle obj-3 immediately
	l4.Release()
	fmt.Printf("stats: %+v\n", c.Stats())

	// Output:
	// load failed: boom
	// shared: obj-1 true
	// after evict, new caller gets: obj-2
	// recycled obj-1
	// double release: leasecache: lease already released
	// after TTL, fresh object: obj-3
	// recycled obj-2
	// recycled obj-3
	// stats: {Loaded:3 Recycled:3}
}
