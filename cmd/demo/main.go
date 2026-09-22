// Command demo simulates a workload against the AIMD limiter. The task
// function, per-round latency and timeout injection are all scripted so the
// run is reproducible.
package main

import (
	"fmt"
	"sync"
	"time"

	"aimd"
)

// task is an injected unit of work: how long it takes and whether it times out.
type task struct {
	latency time.Duration
	timeout bool
}

func main() {
	limiter := aimd.NewLimiter(4, 1, 8, 0.5, 200*time.Millisecond, aimd.RealClock{})

	// Scripted rounds: healthy rounds grow the limit additively, the timeout
	// round cuts it multiplicatively, then it recovers again.
	rounds := [][]task{
		{{latency: 20 * time.Millisecond}, {latency: 30 * time.Millisecond}, {latency: 25 * time.Millisecond}},
		{{latency: 15 * time.Millisecond}, {latency: 20 * time.Millisecond}},
		{{latency: 900 * time.Millisecond, timeout: true}, {latency: 40 * time.Millisecond}},
		{{latency: 25 * time.Millisecond}, {latency: 30 * time.Millisecond}},
		{{latency: 20 * time.Millisecond}, {latency: 20 * time.Millisecond}, {latency: 35 * time.Millisecond}},
	}

	for round, tasks := range rounds {
		var wg sync.WaitGroup
		samples := make([]aimd.Sample, len(tasks))
		for i, tk := range tasks {
			limiter.Acquire() // blocks when inflight reaches the current limit
			wg.Add(1)
			go func(i int, tk task) {
				defer wg.Done()
				defer limiter.Release()
				time.Sleep(tk.latency / 20) // scaled down so the demo runs fast
				samples[i] = aimd.Sample{Latency: tk.latency, Timeout: tk.timeout}
			}(i, tk)
		}
		wg.Wait()

		before := limiter.Limit()
		time.Sleep(200 * time.Millisecond) // let the debounce window elapse
		limiter.Update(samples)
		fmt.Printf("round %d: %d samples (timeout=%v), limit %d -> %d\n",
			round+1, len(samples), hasTimeout(samples), before, limiter.Limit())
	}
}

func hasTimeout(samples []aimd.Sample) bool {
	for _, s := range samples {
		if s.Timeout {
			return true
		}
	}
	return false
}
