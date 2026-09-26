// Command demo synthesizes telemetry locally (no monitoring backend) and runs
// it through the tail sampler, printing every decision and a summary.
package main

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"tailsampler"
)

func verdict(d tailsampler.Decision) string {
	if d.Keep {
		return "KEEP"
	}
	return "DROP"
}

func main() {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := tailsampler.NewManualClock(start)
	s := tailsampler.NewSampler(tailsampler.Config{
		MaxBufferedTraces: 500,
		MaxBufferedSpans:  5000,
		TraceTimeout:      5 * time.Second,
		DecisionTTL:       time.Minute,
		BudgetPerSecond:   20,
		LatencyThreshold:  2 * time.Second,
		Clock:             clock,
	})

	rng := rand.New(rand.NewSource(42))
	emit := func(sp tailsampler.Span) {
		if d, ok := s.OnSpan(sp); ok {
			fmt.Printf("decide trace=%s -> %s (%s)\n", sp.TraceID, verdict(d), d.Reason)
		}
	}

	// Synthesize 60 traces: ~15% with an error span, ~18% slow, a few never
	// completing (timeout path), and one late span after a decision.
	const traces = 60
	for i := 0; i < traces; i++ {
		id := fmt.Sprintf("trace-%03d", i)
		base := start.Add(time.Duration(i*100) * time.Millisecond)
		clock.Set(base)

		dur := time.Duration(50+rng.Intn(200)) * time.Millisecond
		isErr := rng.Float64() < 0.15
		isSlow := !isErr && rng.Float64() < 0.18
		if isSlow {
			dur = 2500 * time.Millisecond
		}
		emit(tailsampler.Span{
			TraceID: id, SpanID: id + "-child", ParentID: id + "-root",
			Name: "db.query", Start: base, End: base.Add(dur / 2), Error: isErr,
		})
		if i%17 == 16 {
			// Incomplete trace: no root span ever arrives; the timeout
			// sweep decides it with the timeout policy.
			continue
		}
		emit(tailsampler.Span{
			TraceID: id, SpanID: id + "-root",
			Name: "http.request", Start: base, End: base.Add(dur),
		})

		// Advance the clock and run the timeout sweep every so often.
		if i%10 == 9 {
			clock.Set(base.Add(6 * time.Second))
			for _, dt := range s.Tick() {
				fmt.Printf("decide trace=%s -> %s (%s)\n", dt.TraceID, verdict(dt.Decision), dt.Decision.Reason)
			}
		}
	}

	// Late span for trace-000, long after it was decided: it must follow the
	// same cached decision instead of being re-buffered.
	late := tailsampler.Span{
		TraceID: "trace-000", SpanID: "trace-000-late", ParentID: "trace-000-root",
		Name: "late.log", Start: start, End: start.Add(10 * time.Millisecond),
	}
	if d, ok := s.OnSpan(late); ok {
		fmt.Printf("late span trace=%s -> %s (%s) [same decision as original]\n", late.TraceID, verdict(d), d.Reason)
	}

	// Consistent decision key across shards for the same trace.
	tid := "trace-007"
	fmt.Printf("decision key trace=%s key=%d shard(4)=%d shard(16)=%d (key identical on every shard)\n",
		tid, tailsampler.DecisionKey(tid), tailsampler.ShardFor(tid, 4), tailsampler.ShardFor(tid, 16))

	fmt.Println()
	fmt.Println("--- summary ---")
	fmt.Printf("kept=%d dropped=%d buffered_spans=%d buffered_traces=%d\n",
		s.Stats.Kept, s.Stats.Dropped, s.BufferedSpans(), s.BufferedTraces())
	reasons := make([]string, 0, len(s.Stats.ByReason))
	for r := range s.Stats.ByReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Printf("  %-24s %d\n", r, s.Stats.ByReason[r])
	}
}
