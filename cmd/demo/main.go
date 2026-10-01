// Demo: adaptive φ-accrual heartbeat detector vs. a naive fixed timeout.
//
// Scenario (simulated time, deterministic):
//  1. A node heartbeats every ~100ms with ±25ms jitter and occasional
//     150ms tail-latency spikes — an imperfect but healthy link.
//  2. A short jitter spike arrives: a fixed 250ms timeout declares the
//     node dead (false positive → needless re-election); the φ detector
//     recognizes the delay as within the observed variance.
//  3. The network truly partitions: the φ detector's suspicion climbs
//     past the threshold and the lease is reclaimed decisively.
package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"phidetector"
)

const (
	phiThreshold = 8.0
	fixedTimeout = 250 * time.Millisecond
)

func main() {
	rng := rand.New(rand.NewSource(42))
	d := phidetector.New(phidetector.WithPhiThreshold(phiThreshold))

	fmt.Println("=== Phase 1: training on a healthy but jittery link ===")
	fmt.Println("    heartbeat ~100ms, jitter ±25ms, 150ms spike every 15 beats")
	now := time.Now()
	d.Report(now)
	maxGap := time.Duration(0)
	for i := 1; i <= 300; i++ {
		gap := 100*time.Millisecond + time.Duration(rng.Int63n(int64(50*time.Millisecond))) - 25*time.Millisecond
		if i%15 == 0 {
			gap += 150 * time.Millisecond
		}
		if gap > maxGap {
			maxGap = gap
		}
		now = now.Add(gap)
		d.Report(now)
	}
	snap := d.Snapshot(now)
	fmt.Printf("    learned distribution: mean=%v stddev=%v over %d samples (max gap %v)\n\n",
		snap.Mean.Round(time.Microsecond), snap.StdDev.Round(time.Microsecond), snap.Samples, maxGap.Round(time.Millisecond))

	fmt.Println("=== Phase 2: short jitter spike (node is ALIVE) ===")
	fmt.Printf("    %-12s %-12s %-28s %s\n", "silence", "phi", "fixed timeout (250ms)", "adaptive detector")
	for _, wait := range []time.Duration{150, 200, 250, 300, 350} {
		at := now.Add(wait * time.Millisecond)
		phi, _ := d.Phi(at)
		fixed := "alive"
		if wait*time.Millisecond >= fixedTimeout {
			fixed = "DEAD (false positive!)"
		}
		adaptive := "alive"
		if d.IsSuspected(at) {
			adaptive = "DEAD"
		}
		fmt.Printf("    %-12s %-12.2f %-28s %s\n", fmt.Sprintf("%dms", wait), phi, fixed, adaptive)
	}
	fmt.Println("    -> fixed timeout would trigger a violent re-election; phi stays calm")
	fmt.Println()

	fmt.Println("=== Phase 3: genuine network partition ===")
	fmt.Printf("    %-12s %-12s %s\n", "silence", "phi", "verdict")
	for _, wait := range []time.Duration{300, 400, 500, 600, 700, 800, 1000} {
		at := now.Add(wait * time.Millisecond)
		phi, _ := d.Phi(at)
		verdict := "alive"
		if d.IsSuspected(at) {
			verdict = "DEAD -> reclaim lease"
		}
		fmt.Printf("    %-12s %-12.2f %s\n", fmt.Sprintf("%dms", wait), phi, verdict)
	}
	fmt.Println()
	fmt.Printf("    phi threshold %.0f  <=>  P(live node this late) < 1e-%0.f\n", phiThreshold, phiThreshold)
	fmt.Println("    " + strings.Repeat("-", 56))
	fmt.Println("    jitter absorbed by history; partition detected decisively")
}
