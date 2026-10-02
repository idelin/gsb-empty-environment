// Command demo simulates a long-tail latency stream and shows the sliding
// window histogram tracking P90/P99, including a spike storm and its
// recovery once the spikes slide out of the window.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"slices"

	"slidinghist"
)

func exactQuantile(samples []float64, q float64) float64 {
	s := slices.Clone(samples)
	slices.Sort(s)
	rank := int(math.Ceil(q * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	return s[rank-1]
}

func main() {
	const capacity = 1000
	w, err := slidinghist.NewWindow(capacity, slidinghist.DefaultConfig())
	if err != nil {
		panic(err)
	}
	r := rand.New(rand.NewSource(1))
	keep := make([]float64, 0, capacity)

	latency := func(i int) float64 {
		switch {
		case i >= 12000 && i < 12030: // spike storm: 30 extreme samples
			return 8000 + r.Float64()*2000
		default: // long-tail body: lognormal, ~20ms median
			return math.Exp(3.0 + 0.9*r.NormFloat64())
		}
	}

	fmt.Printf("window=%d samples, theoretical max relative error=%.2f%%\n\n",
		capacity, w.MaxRelativeError()*100)
	fmt.Printf("%8s %10s %10s %10s %10s %8s\n",
		"sample", "P90(ms)", "P99(ms)", "exactP99", "err%", "note")
	for i := 1; i <= 20000; i++ {
		v := latency(i)
		w.Add(v)
		keep = append(keep, v)
		if len(keep) > capacity {
			keep = keep[1:]
		}
		note := ""
		switch i {
		case 12000:
			note = "<- spikes enter"
		case 12030:
			note = "<- spikes end"
		case 13030:
			note = "<- spikes fully evicted"
		}
		if i%2000 == 0 || note != "" {
			exact := exactQuantile(keep, 0.99)
			errPct := math.Abs(w.P99()-exact) / exact * 100
			fmt.Printf("%8d %10.2f %10.2f %10.2f %9.3f%%  %s\n",
				i, w.P90(), w.P99(), exact, errPct, note)
		}
	}
}
