// Command demo simulates a monitored node over a jittery network, then a
// real partition, showing how the phi-accrual detector rides through delay
// spikes yet decisively reclaims the lease when the node truly goes dark.
package main

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"phiaccrual"
)

func main() {
	cfg := phiaccrual.DefaultConfig()
	cfg.WindowSize = 200
	cfg.Threshold = 8.0
	cfg.MinStdDeviation = 100 * time.Millisecond
	detector := phiaccrual.NewDetector(cfg)

	reclaimed := make(chan float64, 1)
	monitor := phiaccrual.NewMonitor(detector, phiaccrual.Lease{Holder: "node-7"},
		50*time.Millisecond, func(l phiaccrual.Lease, phi float64) {
			fmt.Printf("\n!!! lease of %q RECLAIMED at phi=%.2f (threshold %.0f)\n",
				l.Holder, phi, cfg.Threshold)
			reclaimed <- phi
		})
	stop := make(chan struct{})
	defer close(stop)
	go monitor.Run(stop)

	// Simulated remote node: heartbeats every ~200ms with jitter.
	var partitioned int32
	go func() {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		tick := 0
		for {
			tick++
			interval := 200*time.Millisecond + time.Duration(rng.Intn(81)-40)*time.Millisecond
			if tick%15 == 0 {
				interval = 450 * time.Millisecond // short network delay burst
			}
			time.Sleep(interval)
			if atomic.LoadInt32(&partitioned) == 1 {
				return // cable pulled: heartbeats stop silently
			}
			detector.Report(time.Now())
		}
	}()

	fmt.Println("phase 1: healthy node, jittery link (200ms +/- 40ms, 450ms spikes)")
	fmt.Println("phase 2: network partition at t=6s, heartbeats stop")
	fmt.Println()
	start := time.Now()
	partitionAt := 6 * time.Second
	printed := false
	for {
		elapsed := time.Since(start)
		if !printed && elapsed >= partitionAt {
			atomic.StoreInt32(&partitioned, 1)
			fmt.Printf("\n--- t=%.1fs: PARTITION, heartbeats stop ---\n", elapsed.Seconds())
			printed = true
		}
		phi, err := detector.Phi(time.Now())
		if err == nil {
			state := "ALIVE "
			if !monitor.Reclaimed() && phi >= cfg.Threshold*0.5 {
				state = "SUSPECT"
			}
			if monitor.Reclaimed() {
				state = "DEAD  "
			}
			fmt.Printf("\rt=%5.1fs  phi=%6.2f  %-8s", elapsed.Seconds(), phi, state)
		}
		select {
		case <-reclaimed:
			fmt.Printf("\nt=%.1fs: failure detected %.1fs after the partition\n",
				time.Since(start).Seconds(), time.Since(start).Seconds()-partitionAt.Seconds())
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
