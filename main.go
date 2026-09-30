package main

import (
	"fmt"
	"sort"
)

func main() {
	cfg := SimConfig{
		NumPackets:  6,
		PacketBytes: 100,
		Interval:    3,
		Rate1:       4, // 25 ticks serialization on L1
		Prop1:       10,
		RTO:         60,
		LossEvery:   3, // every 3rd L1 transmission is dropped
		Rate2A:      5,
		Prop2A:      8,
		Rate2B:      2,
		Prop2B:      20,
		MaxAttempts: 5,
		MaxTicks:    100000,
	}
	res := RunSim(cfg)
	fmt.Printf("== simulation ==\n")
	fmt.Printf("packets=%d delivered=%d lost=%d makespan=%d ticks events=%d\n\n",
		cfg.NumPackets, len(res.Delivered), len(res.Lost), res.Makespan, res.Trace.Len())

	bds := AnalyzePackets(res.Trace)
	pkts := make([]int, 0, len(bds))
	for p := range bds {
		pkts = append(pkts, p)
	}
	sort.Ints(pkts)

	fmt.Println("== per-packet delay breakdown (ticks) ==")
	fmt.Printf("%-4s %-5s %-6s %-6s %-7s %-12s %-8s %s\n",
		"pkt", "queue", "serial", "prop", "retrans", "total", "status", "bottleneck")
	var aggQ, aggS, aggP, aggR, aggT int64
	var delivered int
	for _, p := range pkts {
		b := bds[p]
		status := "ok"
		total := fmt.Sprintf("%d", b.Total.Lo)
		if !b.Delivered {
			status = "lost"
			total = fmt.Sprintf(">=%d", b.Total.Lo)
		} else if !b.Total.HiKnown {
			status = "partial"
			total = fmt.Sprintf("%d..?", b.Total.Lo)
		}
		fmt.Printf("%-4d %-5d %-6d %-6d %-7d %-12s %-8s %s\n",
			p, b.Queue, b.Serial, b.Prop, b.RetransWait, total, status, b.Bottleneck)
		if b.Delivered {
			aggQ += b.Queue
			aggS += b.Serial
			aggP += b.Prop
			aggR += b.RetransWait
			aggT += b.Total.Lo
			delivered++
		}
	}
	fmt.Println()

	fmt.Println("== aggregate over delivered packets (success delays only) ==")
	fmt.Printf("queue=%d serial=%d prop=%d retrans=%d total=%d\n", aggQ, aggS, aggP, aggR, aggT)
	agg := map[string]int64{"queue": aggQ, "serial": aggS, "prop": aggP, "retrans": aggR}
	bn, bv := "queue", aggQ
	for k, v := range agg {
		if v > bv {
			bn, bv = k, v
		}
	}
	fmt.Printf("dominant component (bottleneck): %s (%d ticks, %.0f%% of total)\n\n",
		bn, bv, 100*float64(bv)/float64(aggT))

	cp, path := CriticalPath(res.Trace)
	work := TotalWork(res.Trace)
	fmt.Println("== causal DAG metrics ==")
	fmt.Printf("critical path (wall clock): %d ticks over %d events\n", cp, len(path))
	fmt.Printf("  witness path event IDs: %v\n", path)
	fmt.Printf("total work (all phases, overlaps counted): %d ticks\n", work)
	fmt.Printf("parallelism ratio work/critical-path: %.2f\n", float64(work)/float64(cp))

	conflicts, missing := CheckTrace(res.Trace)
	fmt.Printf("conflicts=%d missing-parent-refs=%d\n", len(conflicts), len(missing))
	for _, c := range conflicts {
		fmt.Printf("  conflict: event %d @%d -> event %d @%d (%s)\n",
			c.FromID, c.FromTick, c.ToID, c.ToTick, c.Detail)
	}

	best, npaths, ok := EnumerateCriticalPath(res.Trace, 1_000_000)
	fmt.Printf("exhaustive path enumeration: paths=%d ok=%v max=%d (matches DP: %v)\n",
		npaths, ok, best, ok && best == cp)
}
