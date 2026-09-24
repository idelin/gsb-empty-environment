// Command demo runs three scenarios against the cacheaudit package:
// a consistent cache run, a replayed stale history, and a bounded log that
// overwrites its own evidence.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cacheaudit"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *manualClock { return &manualClock{t: time.Unix(1_700_000_000, 0)} }

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) tick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
}

func printReport(rep cacheaudit.Report) {
	fmt.Println("verdict:", rep.Verdict)
	for _, why := range rep.Incomplete {
		fmt.Println("  evidence gap:", why)
	}
	for _, v := range rep.Violations {
		fmt.Println("  stale read:", v.Detail)
	}
	if rep.Minimal != nil {
		fmt.Printf("  minimal violating subset (%d events, source evidence kept):\n", len(rep.Minimal))
		for _, e := range rep.Minimal {
			fmt.Println("   ", e)
		}
	}
}

func scenarioConsistent() {
	fmt.Println("=== Scenario A: instrumented cache run (expect CONSISTENT) ===")
	clk := newClock()
	var mu sync.Mutex
	versions := map[string]uint64{}
	c := cacheaudit.New(cacheaudit.Options{
		Now: clk.now,
		Load: func(ctx context.Context, key string) (uint64, any, func(), error) {
			mu.Lock()
			versions[key]++
			v := versions[key]
			mu.Unlock()
			return v, fmt.Sprintf("%s-payload-v%d", key, v), func() {}, nil
		},
	})
	ctx := context.Background()

	c.Get(ctx, "alpha") // miss -> load v1 -> publish
	clk.tick()
	c.Get(ctx, "alpha") // hit v1
	clk.tick()
	c.Publish("alpha", 2, "alpha-payload-v2", func() {}) // external update
	clk.tick()
	c.Get(ctx, "alpha") // hit v2
	clk.tick()
	c.Evict("alpha")
	clk.tick()
	c.Get(ctx, "alpha") // miss -> load v3
	c.Close()

	h := c.History()
	for _, e := range h.Events {
		fmt.Println("  ", e)
	}
	st := c.Stats()
	fmt.Printf("stats: %+v\n", st)
	fmt.Printf("hit-rate: %.2f (denominator = all %d Get calls)\n", st.HitRate(), st.Gets)
	printReport(cacheaudit.Audit(h))
	fmt.Println()
}

func scenarioStale() {
	fmt.Println("=== Scenario B: replay a captured stale history (expect STALE) ===")
	base := time.Unix(1_700_000_000, 0)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	h := cacheaudit.History{Events: []cacheaudit.Event{
		{Seq: 1, Kind: cacheaudit.KindPublish, Key: "alpha", Version: 1, At: at(1)},
		{Seq: 2, Kind: cacheaudit.KindCall, Key: "alpha", Version: 1, Start: at(2), End: at(3), Outcome: cacheaudit.OutcomeHit},
		{Seq: 3, Kind: cacheaudit.KindPublish, Key: "alpha", Version: 2, At: at(4)},
		{Seq: 4, Kind: cacheaudit.KindCall, Key: "alpha", Version: 1, Start: at(5), End: at(6), Outcome: cacheaudit.OutcomeHit},
		{Seq: 5, Kind: cacheaudit.KindEvict, Key: "alpha", At: at(7)},
	}}
	for _, e := range h.Events {
		fmt.Println("  ", e)
	}
	printReport(cacheaudit.Audit(h))
	fmt.Println()
}

func scenarioOverwrite() {
	fmt.Println("=== Scenario C: tiny ring overwrites evidence (expect INCONCLUSIVE) ===")
	clk := newClock()
	var v uint64
	c := cacheaudit.New(cacheaudit.Options{
		Now:    clk.now,
		LogCap: 3,
		Load: func(ctx context.Context, key string) (uint64, any, func(), error) {
			v++
			return v, "payload", func() {}, nil
		},
	})
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		c.Get(ctx, "beta")
		clk.tick()
		c.Evict("beta")
		clk.tick()
	}
	c.Close()
	h := c.History()
	fmt.Printf("retained %d of the events (capacity %d, dropped %d):\n",
		len(h.Events), h.Meta.Capacity, h.Meta.Dropped)
	for _, e := range h.Events {
		fmt.Println("  ", e)
	}
	printReport(cacheaudit.Audit(h))
}

func main() {
	scenarioConsistent()
	scenarioStale()
	scenarioOverwrite()
}
