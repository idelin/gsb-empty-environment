// Demo of the negcache package: classified negative caching, generation
// rules, cancellation safety and backoff, all on a fake clock.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"negcache"
)

func main() {
	clock := negcache.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	logger := negcache.LoggerFunc(func(f string, a ...any) {
		fmt.Printf("  log: "+f+"\n", a...)
	})

	var mu sync.Mutex
	db := map[string]string{"user:1": "alice"} // user:2 does not exist
	var calls int
	src := func(_ context.Context, key string) negcache.Response[string] {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if v, ok := db[key]; ok {
			return negcache.Response[string]{Value: v, Version: uint64(calls)}
		}
		if key == "user:flaky" {
			return negcache.Response[string]{Version: uint64(calls),
				Err: negcache.Transient(errors.New("dial tcp: i/o timeout"))}
		}
		return negcache.Response[string]{Version: uint64(calls),
			Err: negcache.NotFound(fmt.Errorf("%s: %w", key, negcache.ErrNotFound))}
	}

	c := negcache.New[string](negcache.Config[string]{
		Source: src, Clock: clock, Logger: logger,
		NotFoundTTL: 10 * time.Second, TransientTTL: time.Second, BackoffMax: 4 * time.Second,
	})
	defer c.Close()
	bg := context.Background()

	fmt.Println("== 1. not-found is cached with class + source version ==")
	for i := 0; i < 2; i++ {
		_, err := c.Get(bg, "tenant-a", "user:2")
		fmt.Printf("get user:2 -> %v\n", err)
	}
	info, _ := c.Inspect("tenant-a", "user:2")
	fmt.Printf("cached: class=%s source-version=%d (source calls so far: %d)\n\n", info.Class, info.Version, calls)

	fmt.Println("== 2. tenant isolation ==")
	c.Get(bg, "tenant-b", "user:2")
	fmt.Printf("tenant-b got its own negative (source calls: %d)\n\n", calls)

	fmt.Println("== 3. network-like error is transient, never not-found ==")
	_, err := c.Get(bg, "tenant-a", "user:flaky")
	fmt.Printf("get user:flaky -> %v\n", err)
	info, _ = c.Inspect("tenant-a", "user:flaky")
	fmt.Printf("cached class=%s ttl=%s\n\n", info.Class, info.SoftExpiry.Sub(clock.Now()))

	fmt.Println("== 4. transient backoff ladder ==")
	for step := 0; step < 3; step++ {
		clock.Advance(info.SoftExpiry.Sub(clock.Now()))
		c.Get(bg, "tenant-a", "user:flaky")
		info, _ = c.Inspect("tenant-a", "user:flaky")
		fmt.Printf("failure %d -> cached ttl=%s\n", step+2, info.SoftExpiry.Sub(clock.Now()))
	}
	fmt.Println()

	fmt.Println("== 5. caller cancellation neither cached nor shared ==")
	gate := make(chan struct{})
	started := make(chan struct{})
	slow := negcache.New[string](negcache.Config[string]{
		Clock: clock, Logger: logger,
		Source: func(context.Context, string) negcache.Response[string] {
			close(started)
			<-gate
			return negcache.Response[string]{Value: "slow-value", Version: 1}
		},
	})
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { _, err := slow.Get(ctx, "t", "k"); done <- err }()
	<-started
	cancel()
	fmt.Printf("impatient caller -> %v\n", <-done)
	close(gate)
	slow.Close()
	v, _ := slow.Get(bg, "t", "k")
	fmt.Printf("next caller hits cached success: %q\n\n", v)

	fmt.Println("== 6. real success overrides negative; late errors cannot ==")
	mu.Lock()
	db["user:2"] = "bob" // record appears in the source
	mu.Unlock()
	clock.Advance(11 * time.Second) // negative expired
	v, _ = c.Get(bg, "tenant-a", "user:2")
	fmt.Printf("get user:2 -> %q (success overrode the negative)\n", v)
	info, _ = c.Inspect("tenant-a", "user:2")
	fmt.Printf("cached: negative=%v version=%d\n", info.Negative, info.Version)
}
