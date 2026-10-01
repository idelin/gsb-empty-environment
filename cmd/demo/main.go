// Command demo synthesizes a telemetry workload locally, replays it through
// the cardinality limiter and prints the resulting statistics plus the
// invariant report. No external monitoring platform is involved.
package main

import (
	"flag"
	"fmt"
	"os"

	"cardinalguard"
)

func main() {
	seed := flag.Int64("seed", 7, "workload seed (deterministic)")
	events := flag.Int("events", 300, "number of synthesized events")
	tenantQuota := flag.Int("tenant-quota", 6, "max live series per tenant")
	metricQuota := flag.Int("metric-quota", 3, "max live series per tenant+metric")
	ttl := flag.Int64("ttl", 15, "series lease in ticks")
	save := flag.String("save", "", "optional path to save the synthesized workload as a replayable fixture")
	flag.Parse()

	workload := cardinalguard.SynthWorkload(*seed, 3, 4, 10, *events)
	if *save != "" {
		if err := cardinalguard.SaveFixture(*save, workload); err != nil {
			fmt.Fprintln(os.Stderr, "save fixture:", err)
			os.Exit(1)
		}
		fmt.Printf("saved %d events to %s\n", len(workload), *save)
	}

	limiter := cardinalguard.NewLimiter(cardinalguard.Config{
		TenantQuota: *tenantQuota,
		MetricQuota: *metricQuota,
		TTL:         *ttl,
		Whitelist:   []cardinalguard.WhitelistEntry{{Tenant: "tenant-0", Metric: "metric-0"}},
	})
	problems, err := cardinalguard.Replay(limiter, workload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}

	snap := limiter.Snapshot()
	fmt.Printf("config: tenant_quota=%d metric_quota=%d ttl=%d whitelist=[tenant-0/metric-0]\n",
		*tenantQuota, *metricQuota, *ttl)
	fmt.Printf("replayed %d events (seed=%d)\n", len(workload), *seed)
	fmt.Println("--- statistics ---")
	fmt.Printf("received:      %6d samples %6d bytes\n", snap.Received, snap.ReceivedBytes)
	fmt.Printf("live:          %6d samples %6d bytes in %d series\n", snap.Live, snap.LiveBytes, snap.LiveSeries)
	fmt.Printf("overflow:      %6d samples %6d bytes (folded total %d)\n", snap.Overflow, snap.OverflowBytes, snap.Folded)
	fmt.Printf("late:          %6d samples %6d bytes\n", snap.Late, snap.LateBytes)
	fmt.Printf("expired:       %6d samples %6d bytes\n", snap.Expired, snap.ExpiredBytes)
	fmt.Printf("promoted:      %6d series from overflow\n", snap.Promoted)
	fmt.Printf("corrected:     %6d label corrections\n", snap.Corrected)
	fmt.Println("--- conservation ---")
	accounted := snap.Live + snap.Overflow + snap.Late + snap.Expired
	fmt.Printf("live+overflow+late+expired = %d, received = %d\n", accounted, snap.Received)
	fmt.Println("--- invariants ---")
	if len(problems) == 0 {
		fmt.Println("OK: all invariants held after every event")
	} else {
		for _, p := range problems {
			fmt.Println("VIOLATION:", p)
		}
		os.Exit(1)
	}
}
