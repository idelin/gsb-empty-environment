// Demo: decaying frequency sketch + admission cache + checkpoint round trip.
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"decayfreq"
)

func main() {
	cfg := decayfreq.Config{
		Rows:          5,
		Width:         4096,
		ByteQuota:     120,
		DecayInterval: time.Second,
	}

	// Deterministic synthetic source, fake clock, close counter: all injected.
	now := time.Unix(1_700_000_000, 0)
	rng := rand.New(rand.NewSource(42))
	const nEvents = 20000
	var events []decayfreq.Event
	for i := 0; i < nEvents; i++ {
		var k string
		if rng.Intn(2) == 0 { // skew: 10 heavy hitters take ~50% of traffic
			k = fmt.Sprintf("hot-%d", rng.Intn(10))
		} else {
			k = fmt.Sprintf("cold-%d", rng.Intn(190))
		}
		events = append(events, decayfreq.Event{ID: fmt.Sprintf("ev-%d", i), Key: k, Size: 10 + rng.Intn(30)})
	}

	// Independent exact reference with the same decay schedule.
	decayed := map[string]float64{}
	for i, e := range events {
		decayed[e.Key]++
		if i%2000 == 1999 {
			for k, v := range decayed {
				decayed[k] = v / 2
			}
		}
	}

	pos, closes := 0, 0
	deps := decayfreq.Deps{
		Next: func() (decayfreq.Event, bool) {
			if pos >= len(events) {
				return decayfreq.Event{}, false
			}
			e := events[pos]
			pos++
			if pos%2000 == 0 {
				now = now.Add(time.Second) // one decay per 2000 events
			}
			return e, true
		},
		Now:   func() time.Time { return now },
		Close: func() error { closes++; return nil },
	}

	p := decayfreq.NewProcessor(cfg, deps)
	if err := p.Run(); err != nil {
		panic(err)
	}

	type row struct {
		key   string
		exact float64
		est   uint32
	}
	var rows []row
	for k, v := range decayed {
		rows = append(rows, row{k, v, p.Sketch().Estimate(k)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].exact > rows[j].exact })

	fmt.Println("== top-8 decayed frequencies: exact reference vs sketch estimate ==")
	for _, r := range rows[:8] {
		fmt.Printf("  key %-9s exact(decayed) %8.1f   estimate %d\n", fmt.Sprintf("%q", r.key), r.exact, r.est)
	}

	fmt.Println("\n== admission cache (byte quota 120) ==")
	used := 0
	for _, e := range p.Cache().Snapshot() {
		fmt.Printf("  %-9q %3d bytes  estimate=%d\n", e.Key, e.Size, p.Sketch().Estimate(e.Key))
		used += e.Size
	}
	fmt.Printf("  used %d / %d bytes\n", used, cfg.ByteQuota)

	// Checkpoint round trip: decisions must be identical after restore.
	var buf bytes.Buffer
	if err := p.Checkpoint(&buf); err != nil {
		panic(err)
	}
	ckptBytes := buf.Len()
	p2, err := decayfreq.RestoreProcessor(cfg, decayfreq.Deps{Now: deps.Now}, &buf)
	if err != nil {
		panic(err)
	}
	probe := []decayfreq.Event{
		{ID: "probe-1", Key: "hot-0", Size: 25},
		{ID: "probe-2", Key: "cold-199", Size: 25},
		{ID: "ev-0", Key: "hot-0", Size: 25}, // duplicate ID: ignored
	}
	d1 := probeDecisions(p, probe)
	d2 := probeDecisions(p2, probe)
	fmt.Printf("\n== checkpoint %d bytes; decisions original %v, restored %v\n", ckptBytes, d1, d2)

	if err := p.Close(); err != nil {
		panic(err)
	}
	fmt.Printf("== resource released: source closed %d time(s)\n", closes)
}

func probeDecisions(p *decayfreq.Processor, events []decayfreq.Event) []bool {
	out := make([]bool, 0, len(events))
	for _, e := range events {
		admitted, dup := p.Process(e)
		out = append(out, admitted && !dup)
	}
	return out
}
