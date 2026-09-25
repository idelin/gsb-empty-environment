// Demo of the soft/hard TTL cache using a real clock and an injectable
// (flaky) data source. Run with: go run ./cmd/demo
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	ttlcache "example.com/ttlcache"
)

func main() {
	var loads atomic.Int32
	source := func(ctx context.Context, key string) (string, error) {
		n := loads.Add(1)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond): // simulate a slow backend
		}
		if n == 3 { // inject one failure to show backoff
			return "", errors.New("backend hiccup")
		}
		return fmt.Sprintf("value#%d", n), nil
	}

	c := ttlcache.New(ttlcache.Config[string, string]{
		Source:  source,
		SoftTTL: 300 * time.Millisecond,
		HardTTL: 900 * time.Millisecond,
		Backoff: func(failures int) time.Duration {
			return time.Duration(failures) * 200 * time.Millisecond
		},
	})
	defer c.Close()

	show := func(tag string, r ttlcache.Result[string], err error) {
		if err != nil {
			fmt.Printf("%-22s error: %v\n", tag, err)
			return
		}
		fmt.Printf("%-22s value=%-8s version=%d stale=%-5v usableUntil=%s\n",
			tag, r.Value, r.Version, r.Stale, r.UsableUntil.Format("15:04:05.000"))
	}

	r, err := c.Get(context.Background(), "user:1") // hard path: initial load
	show("initial load", r, err)

	r, err = c.Get(context.Background(), "user:1") // fresh
	show("fresh read", r, err)

	time.Sleep(400 * time.Millisecond) // now soft-expired
	fmt.Println("-- soft window: 5 concurrent reads share one refresh --")
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := c.Get(context.Background(), "user:1")
			show(fmt.Sprintf("soft read #%d", i+1), r, err)
		}(i)
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond) // let the refresh land

	r, err = c.Get(context.Background(), "user:1")
	show("refreshed read", r, err)

	time.Sleep(400 * time.Millisecond) // soft-expired again; next refresh fails
	r, err = c.Get(context.Background(), "user:1")
	show("stale (refresh fails)", r, err)
	time.Sleep(300 * time.Millisecond)

	time.Sleep(700 * time.Millisecond) // now hard-expired: must wait for fresh
	fmt.Println("-- hard window: read blocks until a fresh value arrives --")
	start := time.Now()
	r, err = c.Get(context.Background(), "user:1")
	show(fmt.Sprintf("hard read (+%v)", time.Since(start).Round(time.Millisecond)), r, err)

	fmt.Printf("total source loads: %d, in-flight at exit: %d\n", loads.Load(), c.InFlight())
}
