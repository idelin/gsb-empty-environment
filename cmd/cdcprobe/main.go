// Command cdcprobe runs a live demo of the CDC pipeline performance
// probe: a source injects heartbeat rows, a simulated link adds
// network/apply latency (with a congestion spike), and a monitor
// measures end-to-end lag, alerts and throttles dynamically.
package main

import (
	"fmt"
	"os"
	"time"

	"cdcprobe"
)

func main() {
	const (
		probeInterval = 100 * time.Millisecond
		threshold     = 1000 * time.Millisecond
	)

	stream := make(chan cdcprobe.Heartbeat, 1024)
	limiter := cdcprobe.NewRateLimiter(20, 2) // 20 hb/s, floor 2 hb/s
	source := cdcprobe.NewSource(probeInterval, limiter, stream)
	link := cdcprobe.NewLink(stream, 50*time.Millisecond, 20*time.Millisecond)
	monitor := cdcprobe.NewMonitor(threshold, limiter, 120)

	go source.Run()
	go link.Run()
	go monitor.Run(link.Out())

	// Live alert printer.
	stopPrinter := make(chan struct{})
	go func() {
		for {
			select {
			case a, ok := <-monitor.Alerts():
				if !ok {
					return
				}
				fmt.Printf("[%s] %s -> throttling source to %.1f hb/s\n",
					a.Time.Format("15:04:05.000"), a.Message, limiter.Rate())
			case <-stopPrinter:
				return
			}
		}
	}()

	// Simulate transient network congestion: lag spikes past 1000ms.
	go func() {
		time.Sleep(3 * time.Second)
		fmt.Println("--- injecting network congestion (+1200ms latency) ---")
		link.SetCongestion(1200 * time.Millisecond)
		time.Sleep(4 * time.Second)
		fmt.Println("--- congestion cleared ---")
		link.SetCongestion(0)
	}()

	// Periodically render the lag curve.
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		fmt.Printf("\033[2J\033[H") // clear screen
		fmt.Printf("t=%ds  injected=%d  rate=%.1f hb/s  throttled=%v\n\n",
			i+1, source.Injected(), limiter.Rate(), limiter.Throttled())
		monitor.RenderChart(os.Stdout, 100, 12)
	}

	source.Stop()
	close(stream) // no more heartbeats: let the link drain
	fmt.Println("\n--- source stopped, draining pipeline ---")
	<-link.Done()
	<-monitor.Done()
	close(stopPrinter)

	fmt.Println("\nFinal lag curve:")
	monitor.RenderChart(os.Stdout, 100, 12)
	fmt.Printf("\nTotal heartbeats injected: %d\n", source.Injected())
	fmt.Printf("Total alerts raised:       %d\n", len(monitor.AlertLog()))
}
