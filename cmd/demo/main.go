// Demo: compressed bitmap operations at billion scale, plus a
// multi-tenant DRR fair-scheduling run.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"bitmapsched/bitmap"
	"bitmapsched/scheduler"
)

func bitmapDemo() {
	fmt.Println("=== Compressed bitmap demo ===")

	// Sparse, discrete IDs -> array containers.
	sparse := bitmap.New()
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 100000; i++ {
		sparse.Add(r.Uint32())
	}
	a, b, rn := sparse.ContainerSummary()
	fmt.Printf("sparse 100k random IDs : containers array=%d bitmap=%d run=%d cardinality=%d\n",
		a, b, rn, sparse.Cardinality())

	// Dense contiguous interval -> RLE containers.
	dense := bitmap.New()
	dense.AddRange(10000000, 20000000)
	a, b, rn = dense.ContainerSummary()
	fmt.Printf("dense [10M,20M) range  : containers array=%d bitmap=%d run=%d cardinality=%d\n",
		a, b, rn, dense.Cardinality())

	// Billion-scale simulation: two bitmaps covering most of the
	// 32-bit ID space (4.29 billion IDs), intersected/unioned via
	// interval-level RLE operations.
	x := bitmap.New()
	x.AddRange(0, 3500000000)
	y := bitmap.New()
	y.AddRange(1000000000, 1<<32)

	start := time.Now()
	inter := bitmap.And(x, y)
	fmt.Printf("AND |x|=3.5e9 |y|=3.29e9 -> |inter|=%d in %v\n",
		inter.Cardinality(), time.Since(start))

	start = time.Now()
	union := bitmap.Or(x, y)
	fmt.Printf("OR  -> |union|=%d in %v\n", union.Cardinality(), time.Since(start))

	start = time.Now()
	diff := bitmap.Xor(x, y)
	fmt.Printf("XOR -> |diff|=%d in %v\n", diff.Cardinality(), time.Since(start))

	// Batch operations.
	m1, m2, m3 := bitmap.New(), bitmap.New(), bitmap.New()
	m1.AddRange(0, 1000)
	m2.AddRange(500, 1500)
	m3.AddRange(900, 2000)
	fmt.Printf("batch AndAll([0,1000),[500,1500),[900,2000)) -> %v\n",
		bitmap.AndAll(m1, m2, m3).ToSlice())
	fmt.Printf("batch OrAll cardinality -> %d\n", bitmap.OrAll(m1, m2, m3).Cardinality())
	fmt.Println()
}

func schedulerDemo() {
	fmt.Println("=== Multi-tenant DRR scheduler demo ===")
	s := scheduler.New(4, 1)
	s.RegisterTenant("analytics", scheduler.TenantConfig{Weight: 1, MaxConcurrent: 8, MaxQueue: 10000})
	s.RegisterTenant("billing", scheduler.TenantConfig{Weight: 3, MaxConcurrent: 8, MaxQueue: 10000})

	var analyticsDone, billingDone int64
	mk := func(c *int64) func(context.Context) {
		return func(ctx context.Context) {
			atomic.AddInt64(c, 1)
			time.Sleep(time.Millisecond)
		}
	}
	const n = 300
	for i := 0; i < n; i++ {
		if err := s.Submit(scheduler.Task{Tenant: "analytics", Run: mk(&analyticsDone)}); err != nil {
			fmt.Println("submit analytics:", err)
		}
		if err := s.Submit(scheduler.Task{Tenant: "billing", Run: mk(&billingDone)}); err != nil {
			fmt.Println("submit billing:", err)
		}
	}

	// Sample mid-stream while both tenants are saturated.
	time.Sleep(300 * time.Millisecond)
	la, lb := atomic.LoadInt64(&analyticsDone), atomic.LoadInt64(&billingDone)
	fmt.Printf("mid-stream (weights 1:3): analytics=%d billing=%d ratio=%.2f\n",
		la, lb, float64(lb)/float64(la))

	for atomic.LoadInt64(&analyticsDone)+atomic.LoadInt64(&billingDone) < 2*n {
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()
	for _, id := range []string{"analytics", "billing"} {
		st, _ := s.Stats(id)
		fmt.Printf("tenant %-9s submitted=%d completed=%d rejected=%d inflight=%d queued=%d\n",
			id, st.Submitted, st.Completed, st.Rejected, st.Inflight, st.Queued)
	}
}

func main() {
	bitmapDemo()
	schedulerDemo()
}
