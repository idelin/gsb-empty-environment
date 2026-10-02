// Command demo runs a live CDC probe simulation: heartbeats are injected
// into the source, replicated over a link that suffers a transient
// congestion episode (lag > 1000ms), and the monitor alerts, throttles
// the source, and finally renders the lag curve.
package main

import (
	"fmt"
	"time"

	"cdcprobe"
)

func main() {
	const (
		baseRate     = 20.0 // heartbeats per second
		throttleRate = 2.0  // throttled heartbeats per second
		threshold    = cdcprobe.DefaultLagThreshold
	)

	src := cdcprobe.NewStore()
	dst := cdcprobe.NewStore()
	limiter := cdcprobe.NewRateLimiter(baseRate)
	injector := cdcprobe.NewInjector(src, limiter, 20*time.Millisecond)

	// Network model: 5ms baseline; a transient congestion episode between
	// t=2s and t=4s adds 1500ms of delay per row, pushing lag past 1000ms.
	start := time.Now()
	latency := func() time.Duration {
		if el := time.Since(start); el > 2*time.Second && el < 4*time.Second {
			return 1500 * time.Millisecond
		}
		return 5 * time.Millisecond
	}
	replicator := cdcprobe.NewReplicator(src, dst, latency)

	monitor := cdcprobe.NewMonitor(dst, limiter, threshold, baseRate, throttleRate)
	monitor.OnAlert(func(a cdcprobe.Alert) {
		fmt.Printf("[%6.1fs] ALERT: %s -> throttle source to %.0f hb/s\n",
			time.Since(start).Seconds(), a.Msg, throttleRate)
	})
	monitor.OnRecover(func() {
		fmt.Printf("[%6.1fs] RECOVERED: lag back below %v -> restore source to %.0f hb/s\n",
			time.Since(start).Seconds(), threshold/2, baseRate)
	})

	fmt.Println("starting CDC probe: 20 hb/s, lag threshold 1000ms, congestion at t=2s..4s")
	monitor.Start()
	replicator.Start()
	injector.Start()

	// Live lag readout once per second.
	for i := 0; i < 8; i++ {
		time.Sleep(time.Second)
		samples := monitor.Samples()
		if len(samples) == 0 {
			continue
		}
		last := samples[len(samples)-1]
		fmt.Printf("[%6.1fs] lag=%8v  injected=%d applied=%d rate=%.0f hb/s\n",
			time.Since(start).Seconds(), last.Lag.Round(time.Millisecond),
			injector.Injected(), replicator.Applied(), limiter.Rate())
	}

	injector.Stop()
	replicator.Stop()
	monitor.Stop()

	samples := monitor.Samples()
	fmt.Printf("\nsummary: injected=%d applied=%d samples=%d max-lag=%v alerts=%d\n\n",
		injector.Injected(), replicator.Applied(), len(samples),
		monitor.MaxLag().Round(time.Millisecond), len(monitor.Alerts()))
	fmt.Print(cdcprobe.Plot(samples, threshold, 72, 12))
}
