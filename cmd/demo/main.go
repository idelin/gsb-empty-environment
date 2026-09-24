// Command demo simulates a workload: an explicit access sequence drives a
// Markov predictor; prefetch runs at low priority under a shared budget
// while real requests promote/merge with it.
package main

import (
	"context"
	"fmt"
	"time"

	"prefetch"
)

func main() {
	clock := prefetch.NewManualClock(time.Now())

	// Injectable data source: 20ms latency, versioned values, and an
	// injectable failure for key "boom".
	versions := map[string]uint64{}
	src := prefetch.SourceFunc(func(ctx context.Context, key string) (prefetch.Value, error) {
		if key == "boom" {
			return prefetch.Value{}, fmt.Errorf("injected failure for %q", key)
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			return prefetch.Value{}, ctx.Err()
		}
		versions[key]++
		return prefetch.Value{Data: "data-" + key, Version: versions[key]}, nil
	})

	// Tiny foreground segment (2) so prefetch actually matters; prefetch
	// segment (2) absorbs speculation without polluting foreground.
	cache := prefetch.NewCache(clock, 2, 2, time.Minute)
	sched := prefetch.NewScheduler(src, cache, 3, 1) // 3 slots, 1 reserved for foreground
	engine := prefetch.NewEngine(sched, 2, prefetch.MarkovPredictor{})

	ctx := context.Background()
	trace := []string{"a", "b", "c", "a", "b", "c", "boom", "a", "b"}
	for _, key := range trace {
		_, hit := cache.Get(key)
		v, err := engine.Access(ctx, key)
		st := sched.Stats()
		if err != nil {
			fmt.Printf("access %-4s -> ERROR %v\n", key, err)
		} else {
			tag := "MISS"
			if hit {
				tag = "HIT "
			}
			fmt.Printf("access %-4s -> %s %v (v%d)  [gen=%d fgRun=%d pfRun=%d pending=%d]\n",
				key, tag, v.Data, v.Version, st.Generation, st.FGRunning, st.PFRunning, st.Pending)
		}
		time.Sleep(30 * time.Millisecond) // let this round's prefetch land
	}

	// Invalidate "b": any stale in-flight prefetch result for it is dropped.
	cache.Invalidate("b", versions["b"])
	fmt.Printf("\ninvalidated b at version %d\n", versions["b"])

	sched.Close() // waits for in-flight loads to unwind
	fg, pf := cache.Len()
	fmt.Printf("final cache: foreground=%d prefetch-segment=%d\n", fg, pf)
	if _, ok := cache.Get("b"); ok {
		fmt.Println("BUG: invalidated b was resurrected")
	} else {
		fmt.Println("ok: invalidated b was not resurrected")
	}
}
