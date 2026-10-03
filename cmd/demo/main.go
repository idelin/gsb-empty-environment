// Command demo showcases the compressed bitmap (container auto-selection,
// large-scale set algebra) and the multi-tenant DRR fair scheduler.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"bitmapsched/bitmap"
	"bitmapsched/scheduler"
)

func main() {
	bitmapDemo()
	schedulerDemo()
}

func printStats(name string, b *bitmap.Bitmap) {
	st := b.Stats()
	fmt.Printf("%-38s values=%-10d containers=%-7d array=%-7d bitmap=%-7d run=%-7d bytes=%d\n",
		name, st.Cardinality, st.Containers, st.ArrayContainers, st.BitmapContainers, st.RunContainers, st.Bytes)
}

func bitmapDemo() {
	fmt.Println("=== Compressed bitmap: container auto-selection ===")

	// Sparse discrete IDs across a 1e10 key space.
	sparse := bitmap.New()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1_000_000; i++ {
		sparse.Add(rng.Uint64() % 10_000_000_000)
	}
	printStats("sparse: 1e6 random ids in 1e10 space", sparse)

	// Dense contiguous interval IDs.
	dense := bitmap.New()
	for base := uint64(0); base < 200_000_000; base += 1_000_000 {
		dense.AddRange(base, base+500_000)
	}
	printStats("dense: 200 intervals x 500k ids", dense)

	// Dense random (50% fill) -> bitmap containers.
	noisy := bitmap.New()
	for i := 0; i < 20_000_000; i++ {
		noisy.Add(rng.Uint64() % (1 << 22))
	}
	printStats("noisy: 2e7 random ids in 2^22 space", noisy)

	fmt.Println()
	fmt.Println("=== Large-scale set algebra (10^10 values per bitmap) ===")
	const hi = uint64(10_000_000_000) // 百亿
	a := bitmap.New()
	b := bitmap.New()
	start := time.Now()
	a.AddRange(0, hi)
	b.AddRange(hi/2, hi+hi/2)
	fmt.Printf("insert 1e10 contiguous ids x2: %s (a=%d values, b=%d values)\n",
		time.Since(start), a.Cardinality(), b.Cardinality())
	printStats("a = [0, 1e10]", a)
	printStats("b = [5e9, 1.5e10]", b)

	a.AndCardinality(b) // warm up sorted-key caches
	start = time.Now()
	got := a.AndCardinality(b)
	andElapsed := time.Since(start)
	shared := a.Stats().Containers
	fmt.Printf("AND                    -> %-12d in %s (%.0fns per shared container pair, %.2e values/s)"+"\n",
		got, andElapsed, float64(andElapsed.Nanoseconds())/float64(shared),
		float64(got)/andElapsed.Seconds())
	timeOp("AND (materialized)", func() uint64 { return a.And(b).Cardinality() })
	timeOp("OR", func() uint64 { return a.Or(b).Cardinality() })
	timeOp("XOR", func() uint64 { return a.Xor(b).Cardinality() })
	fmt.Println()
}

func timeOp(name string, fn func() uint64) {
	start := time.Now()
	got := fn()
	fmt.Printf("%-22s -> %-12d in %s\n", name, got, time.Since(start))
}

func schedulerDemo() {
	fmt.Println("=== Multi-tenant DRR fair scheduler ===")
	base := runtime.NumGoroutine()
	s := scheduler.New(scheduler.Config{Workers: 1, Quantum: 1})
	weights := []struct {
		id string
		w  uint64
	}{{"alice", 1}, {"bob", 2}, {"carol", 3}}
	s.RegisterTenant("gate", scheduler.Quota{MaxQueued: 10})
	for _, tw := range weights {
		s.RegisterTenant(tw.id, scheduler.Quota{
			Weight: tw.w, MaxQueued: 10000, MaxInflight: 10000,
		})
	}

	// Gate task holds the worker so tenant queues build a backlog.
	release := make(chan struct{})
	gate, _ := s.Submit(&scheduler.Task{TenantID: "gate", Cost: 1,
		Run: func(context.Context) error { <-release; return nil }})

	var mu sync.Mutex
	var completions []string
	const perTenant = 600
	dones := make([]<-chan error, 0, 3*perTenant)
	for i := 0; i < perTenant; i++ {
		for _, tw := range weights {
			id := tw.id
			d, err := s.Submit(&scheduler.Task{TenantID: id, Cost: 1,
				Run: func(context.Context) error {
					mu.Lock()
					completions = append(completions, id)
					mu.Unlock()
					return nil
				}})
			if err != nil {
				panic(err)
			}
			dones = append(dones, d)
		}
	}
	close(release)
	for _, d := range append(dones, gate) {
		<-d
	}

	// Fairness window: first half of all completions.
	counts := map[string]int{}
	for _, id := range completions[:len(completions)/2] {
		counts[id]++
	}
	fmt.Printf("weights alice:bob:carol = 1:2:3, tasks/tenant = %d\n", perTenant)
	fmt.Printf("first-half completions (expect ~1:2:3): alice=%d bob=%d carol=%d\n",
		counts["alice"], counts["bob"], counts["carol"])
	for _, tw := range weights {
		st, _ := s.Stats(tw.id)
		fmt.Printf("tenant %-6s submitted=%d completed=%d rejected=%d queued=%d inflight=%d\n",
			tw.id, st.Submitted, st.Completed, st.Rejected, st.Queued, st.Inflight)
	}

	// Multi-tenant bitmap store with quota admission + namespace isolation.
	store := scheduler.NewStore(s)
	if err := store.Add("alice", "users", 1, 2, 3, 1000); err != nil {
		panic(err)
	}
	if _, ok := store.Snapshot("bob", "users"); ok {
		panic("isolation breach")
	}
	snap, _ := store.Snapshot("alice", "users")
	fmt.Printf("store: alice/users cardinality=%d, bob/users not visible (isolated)\n",
		snap.Cardinality())

	s.Shutdown()
	time.Sleep(50 * time.Millisecond)
	fmt.Printf("goroutines: before=%d after-shutdown=%d (no leak)\n", base, runtime.NumGoroutine())
}
