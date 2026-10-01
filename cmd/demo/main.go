// demo 在本地合成一批遥测事件，回放经过基数限制器，
// 打印每个租户/指标的存活系列、溢出折叠与封存代际，并核验不变量。
package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"telemetry"
)

func main() {
	cfg := telemetry.Config{
		TenantQuota: 6,
		MetricQuota: 3,
		TTL:         2 * time.Second,
	}
	if err := cfg.Allow("http_requests", telemetry.Label{Key: "instance", Value: "0"}, telemetry.Label{Key: "job", Value: "ingest"}); err != nil {
		panic(err)
	}

	events := telemetry.SynthEvents(42, 1200)
	fmt.Printf("replaying %d synthesized events (tenant quota=%d, metric quota=%d, ttl=%s)\n\n",
		len(events), cfg.TenantQuota, cfg.MetricQuota, cfg.TTL)

	snap := telemetry.Replay(cfg, events)

	tenants := make([]string, 0, len(snap.Tenants))
	for name := range snap.Tenants {
		tenants = append(tenants, name)
	}
	sort.Strings(tenants)
	for _, tname := range tenants {
		tn := snap.Tenants[tname]
		fmt.Printf("tenant %s: live(counted)=%d ledger=%dB\n", tname, tn.LiveCounted, tn.LedgerBytes)
		metrics := make([]string, 0, len(tn.Metrics))
		for name := range tn.Metrics {
			metrics = append(metrics, name)
		}
		sort.Strings(metrics)
		for _, mname := range metrics {
			m := tn.Metrics[mname]
			var archivedSamples int64
			for _, a := range m.Archived {
				archivedSamples += a.Samples
			}
			fmt.Printf("  %-14s live=%d overflow=%d folded=%d retracted=%d archived=%d gens (%d samples) received=%d rejected=%d\n",
				mname, m.LiveCounted, m.OverflowSamples, m.FoldedTotal, m.RetractedSum,
				len(m.Archived), archivedSamples, m.Received, m.Rejected)
		}
	}

	fmt.Println()
	problems := telemetry.Verify(snap)
	if len(problems) > 0 {
		fmt.Println("INVARIANT VIOLATIONS:")
		for _, p := range problems {
			fmt.Println("  -", p)
		}
		os.Exit(1)
	}
	fmt.Println("all invariants hold: conservation, generation isolation, ledger consistency, quota fairness")
}
