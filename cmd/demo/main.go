// Command demo streams a long-tail latency workload through the
// sliding-window histogram, injects extreme spikes mid-stream, and
// prints estimated vs. exact (full-retention) P50/P90/P99 so the
// spike eviction and recovery behaviour is visible.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"swhist"
)

func exactQuantile(samples []float64, q float64) float64 {
	s := make([]float64, len(samples))
	copy(s, samples)
	sort.Float64s(s)
	rank := int(math.Ceil(q * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	return s[rank-1]
}

func main() {
	const capacity = 1000
	h, err := swhist.New(swhist.DefaultConfig())
	if err != nil {
		panic(err)
	}
	fmt.Printf("window=%d buckets=%d (linear 0-200ms @0.05ms, log x1.002 up to 600s)\n\n",
		capacity, h.NumBuckets())

	r := rand.New(rand.NewSource(1))
	next := func() float64 { return math.Exp(math.Log(20) + 0.8*r.NormFloat64()) } // lognormal, ms

	window := make([]float64, 0, capacity)
	push := func(v float64) {
		h.Add(v)
		window = append(window, v)
		if len(window) > capacity {
			window = window[1:]
		}
	}
	report := func(label string) {
		fmt.Printf("%-28s est  P50=%8.2fms P90=%8.2fms P99=%8.2fms\n", label,
			h.Quantile(0.50), h.P90(), h.P99())
		fmt.Printf("%-28s exact P50=%8.2fms P90=%8.2fms P99=%8.2fms  (P99 err %.3f%%)\n\n", "",
			exactQuantile(window, 0.50), exactQuantile(window, 0.90), exactQuantile(window, 0.99),
			math.Abs(h.P99()-exactQuantile(window, 0.99))/exactQuantile(window, 0.99)*100)
	}

	for i := 0; i < capacity; i++ {
		push(next())
	}
	report("warmup: 1000 long-tail samples")

	for i := 0; i < 5000; i++ {
		push(next())
	}
	report("steady state after 5000 more")

	for i := 0; i < 20; i++ {
		push(30000) // 30s spikes, 2% of the window
	}
	report("spike: 20x 30000ms injected")

	for i := 0; i < 500; i++ {
		push(next())
	}
	report("spikes half expired (+500)")

	for i := 0; i < 500; i++ {
		push(next())
	}
	report("spikes fully expired (+1000)")
}
