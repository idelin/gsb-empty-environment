// Command demo walks through the negative-cache behavior with a scripted
// in-memory source: not-found caching, temporary-error backoff, success
// overwriting a negative, tenant isolation, and cancellation handling.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"negcache"
)

func main() {
	var mu sync.Mutex
	store := map[string][]byte{} // the "remote" source of truth
	calls := map[string]int{}    // source call count per key
	tempDown := map[string]int{} // remaining simulated outages per key

	src := func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[tenant+"/"+key]++
		if tempDown[key] > 0 {
			tempDown[key]--
			return nil, 0, &negcache.TempError{Msg: "simulated network timeout"}
		}
		if v, ok := store[key]; ok {
			return v, 1, nil
		}
		return nil, 1, negcache.ErrNotFound
	}

	c := negcache.New(negcache.Config{
		Source:       src,
		NotFoundTTL:  300 * time.Millisecond,
		TempBaseTTL:  100 * time.Millisecond,
		TempMaxTTL:   400 * time.Millisecond,
		PermanentTTL: 2 * time.Second,
		HardTTL:      5 * time.Second,
		Capacity:     100,
		Log: func(le negcache.LogEntry) {
			kind := "source-error"
			if le.Cancellation {
				kind = "cancellation"
			}
			fmt.Printf("  [log] %-12s tenant=%s key=%s class=%s msg=%q\n",
				kind, le.Tenant, le.Key, le.Class, le.Msg)
		},
	})
	defer c.Close()

	show := func(label, tenant, key string) {
		r, err := c.Get(context.Background(), tenant, key)
		switch {
		case err != nil:
			fmt.Printf("%-34s -> err=%v\n", label, err)
		case r.Negative:
			fmt.Printf("%-34s -> NEGATIVE class=%s srcVer=%d fromCache=%v (source calls=%d)\n",
				label, r.Class, r.SourceVersion, r.FromCache, calls[tenant+"/"+key])
		default:
			fmt.Printf("%-34s -> value=%q srcVer=%d fromCache=%v (source calls=%d)\n",
				label, r.Value, r.SourceVersion, r.FromCache, calls[tenant+"/"+key])
		}
	}

	fmt.Println("== 1. not-found is cached as a negative entry ==")
	show("get alice/missing (miss)", "alice", "missing")
	show("get alice/missing (hit)", "alice", "missing")

	fmt.Println("== 2. negative results are tenant-isolated ==")
	store["missing"] = []byte("hello")
	show("get bob/missing (own fill)", "bob", "missing")

	fmt.Println("== 3. temporary errors back off, never cached as not-found ==")
	tempDown["flaky"] = 3
	show("get alice/flaky (temp #1)", "alice", "flaky")
	time.Sleep(150 * time.Millisecond)
	show("get alice/flaky (temp #2)", "alice", "flaky")
	time.Sleep(250 * time.Millisecond)
	show("get alice/flaky (temp #3)", "alice", "flaky")
	time.Sleep(450 * time.Millisecond)
	store["flaky"] = []byte("recovered")
	show("get alice/flaky (recovered)", "alice", "flaky")

	fmt.Println("== 4. a real success overwrites a negative entry ==")
	show("get alice/ghost (not-found)", "alice", "ghost")
	store["ghost"] = []byte("now-exists")
	c.Invalidate("alice", "ghost")
	show("get alice/ghost (after write)", "alice", "ghost")

	fmt.Println("== 5. one caller canceling does not pollute others ==")
	started := make(chan struct{})
	release := make(chan struct{})
	slow := negcache.New(negcache.Config{
		Source: func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
			close(started)
			select {
			case <-release:
				return []byte("shared-value"), 1, nil
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			}
		},
		NotFoundTTL: time.Second, TempBaseTTL: time.Second, TempMaxTTL: time.Second,
		PermanentTTL: time.Second, HardTTL: 5 * time.Second, Capacity: 10,
	})
	defer slow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	waiter1 := make(chan error, 1)
	go func() { _, err := slow.Get(ctx, "t", "slow"); waiter1 <- err }()
	<-started
	waiter2 := make(chan negcache.Result, 1)
	go func() { r, _ := slow.Get(context.Background(), "t", "slow"); waiter2 <- r }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	fmt.Printf("  waiter1 (canceled)          -> %v\n", <-waiter1)
	close(release)
	r2 := <-waiter2
	fmt.Printf("  waiter2 (shared result)     -> value=%q\n", r2.Value)
	r, _ := slow.Get(context.Background(), "t", "slow")
	fmt.Printf("  follow-up get               -> value=%q fromCache=%v\n", r.Value, r.FromCache)
}
