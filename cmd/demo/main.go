// Command demo shows the coalescing invalidation cache end to end with a
// manual clock, so the output is deterministic: merged invalidations,
// rate-limited batches, critical-lane priority, conservative invalidation on
// a full queue, failure retry, and clean shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"coalesce"
)

// source is a fake data source: it counts calls and fails on demand.
type source struct {
	mu     sync.Mutex
	calls  int
	batch  [][]string
	vers   map[string]uint64
	failed int
}

func newSource() *source { return &source{vers: map[string]uint64{}} }

func (s *source) load(_ context.Context, keys []string) (map[string]coalesce.LoadResult[string], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.batch = append(s.batch, append([]string(nil), keys...))
	if s.failed > 0 {
		s.failed--
		return nil, errors.New("data source temporarily unavailable")
	}
	out := make(map[string]coalesce.LoadResult[string], len(keys))
	for _, k := range keys {
		out[k] = coalesce.LoadResult[string]{Value: "value-of-" + k, Version: s.vers[k]}
	}
	return out, nil
}

func (s *source) batches() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.batch...)
}

func waitOK(c *coalesce.Cache[string, string], key string) (string, uint64) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ver, err := c.Get(key); err == nil {
			return v, ver
		}
		time.Sleep(time.Millisecond)
	}
	return "<invalid>", 0
}

func newCache(src *source, batch, cap int) (*coalesce.Cache[string, string], *coalesce.ManualClock) {
	clk := coalesce.NewManualClock()
	return coalesce.New(coalesce.Config[string, string]{
		Load:          src.load,
		Clock:         clk,
		Interval:      time.Millisecond,
		BatchSize:     batch,
		QueueCapacity: cap,
	}), clk
}

func main() {
	// 1. A burst of invalidations for the same key merges into one load.
	{
		src := newSource()
		src.vers["user:1"] = 10
		c, clk := newCache(src, 8, 16)
		for i := 0; i < 1000; i++ {
			_ = c.Invalidate("user:1", 10)
		}
		clk.Advance(time.Millisecond)
		v, ver := waitOK(c, "user:1")
		fmt.Printf("[coalesce] 1000 invalidations of user:1 -> %d data-source call(s); Get=%q v%d\n",
			src.calls, v, ver)
		c.Close()
	}

	// 2. Rate limiting: one batch per tick, critical keys first.
	{
		src := newSource()
		for _, k := range []string{"n1", "n2", "n3", "c1", "c2"} {
			src.vers[k] = 1
		}
		c, clk := newCache(src, 2, 16)
		_ = c.Invalidate("n1", 1)
		_ = c.Invalidate("n2", 1)
		_ = c.Invalidate("n3", 1)
		_ = c.InvalidateCritical("c1", 1)
		_ = c.InvalidateCritical("c2", 1)
		for i := 0; i < 3; i++ {
			clk.Advance(time.Millisecond)
			time.Sleep(5 * time.Millisecond)
		}
		fmt.Printf("[priority+ratelimit] batches dispatched one per tick: %v\n", src.batches())
		c.Close()
	}

	// 3. Full queue: the overflowing key is conservatively invalidated (its
	// old value stops serving) and recovers once space frees up.
	{
		src := newSource()
		src.vers["seed"] = 1
		c, clk := newCache(src, 2, 2)
		_ = c.Invalidate("seed", 1)
		clk.Advance(time.Millisecond)
		waitOK(c, "seed")

		_ = c.Invalidate("a", 1)
		_ = c.Invalidate("b", 1) // queue now full (capacity 2)
		src.vers["a"], src.vers["b"] = 1, 1
		_ = c.Invalidate("seed", 2) // overflows -> deferred, old value dropped
		_, _, err := c.Get("seed")
		fmt.Printf("[queue-full] Get(seed) while queue full: %v (old value must not serve)\n", err)

		src.mu.Lock()
		src.vers["seed"] = 2
		src.mu.Unlock()
		for i := 0; i < 3; i++ {
			clk.Advance(time.Millisecond)
			time.Sleep(5 * time.Millisecond)
		}
		v, ver := waitOK(c, "seed")
		fmt.Printf("[queue-full] after draining, deferred key recovered: Get=%q v%d\n", v, ver)
		c.Close()
	}

	// 4. Failure injection: a failed load confirms nothing and is retried.
	{
		src := newSource()
		src.vers["k"] = 1
		src.failed = 1
		var errCount int
		var mu sync.Mutex
		clk := coalesce.NewManualClock()
		c := coalesce.New(coalesce.Config[string, string]{
			Load: src.load, Clock: clk, Interval: time.Millisecond,
			BatchSize: 4, QueueCapacity: 8,
			OnError: func(keys []string, err error) {
				mu.Lock()
				errCount++
				mu.Unlock()
			},
		})
		_ = c.Invalidate("k", 1)
		clk.Advance(time.Millisecond) // fails
		time.Sleep(5 * time.Millisecond)
		_, _, err := c.Get("k")
		clk.Advance(time.Millisecond) // retry succeeds
		v, ver := waitOK(c, "k")
		fmt.Printf("[failure-retry] after failure Get=%v, errors reported=%d, then recovered: %q v%d\n",
			err, errCount, v, ver)
		c.Close()
	}

	// 5. Clean shutdown releases the worker goroutine.
	{
		src := newSource()
		c, _ := newCache(src, 4, 8)
		before := runtime.NumGoroutine()
		c.Close()
		time.Sleep(20 * time.Millisecond)
		fmt.Printf("[shutdown] goroutines before Close=%d after=%d (worker and ticker released)\n",
			before, runtime.NumGoroutine())
	}
}
