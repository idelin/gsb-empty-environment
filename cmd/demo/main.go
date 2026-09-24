// Command demo runs a decaying frequency sketch over a synthetic Zipf
// stream and compares its estimates against an exact reference.
package main

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"freqsketch"
)

func main() {
	sketch := freqsketch.NewSketch(freqsketch.SketchConfig{})
	table := freqsketch.NewTable(sketch, 400) // ~20 entries of 20 bytes

	// Injectable logical clock: 1s per 2000 events, decay every second.
	clock := time.Unix(0, 0)
	processed := 0
	pipe := freqsketch.NewPipeline(freqsketch.PipelineConfig{
		Sketch:     sketch,
		Table:      table,
		Clock:      func() time.Time { return clock },
		DecayEvery: time.Second,
	})
	defer pipe.Close()

	// Exact reference with matching decay, for error evaluation.
	exact := map[string]uint64{}
	lastDecay := time.Unix(0, 0)

	r := rand.New(rand.NewSource(42))
	z := rand.NewZipf(r, 1.2, 1, 999)
	const n = 100000
	for i := 0; i < n; i++ {
		key := "item-" + strconv.FormatUint(z.Uint64(), 10)
		ev := freqsketch.Event{
			ID:    "ev-" + strconv.Itoa(i),
			Key:   key,
			Bytes: 20,
		}
		if err := pipe.Process(ev); err != nil {
			panic(err)
		}
		// Mirror the pipeline's declared logical order on the exact
		// reference: decay first (if the clock crossed a boundary),
		// then count the event.
		for !clock.Before(lastDecay.Add(time.Second)) {
			for k := range exact {
				exact[k] >>= 1
			}
			lastDecay = lastDecay.Add(time.Second)
		}
		exact[key]++
		processed++
		if processed%2000 == 0 {
			clock = clock.Add(time.Second)
		}
	}

	fmt.Printf("stream: %d events, %d distinct keys, %d decay epochs\n\n",
		n, len(exact), sketch.Epoch())

	// Top-10 by decayed exact count vs sketch estimate.
	type kv struct {
		k string
		v uint64
	}
	ranked := make([]kv, 0, len(exact))
	for k, v := range exact {
		ranked = append(ranked, kv{k, v})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].v != ranked[j].v {
			return ranked[i].v > ranked[j].v
		}
		return ranked[i].k < ranked[j].k
	})
	fmt.Println("top-10 (decayed exact vs sketch upper-bound estimate):")
	var absErr uint64
	for _, e := range ranked {
		absErr += sketch.Estimate(e.k) - e.v
	}
	for i := 0; i < 10 && i < len(ranked); i++ {
		e := ranked[i]
		fmt.Printf("  %-10s exact=%-6d estimate=%-6d\n", e.k, e.v, sketch.Estimate(e.k))
	}
	fmt.Printf("\ntotal absolute error: %d (%.2f%% of decayed stream mass)\n",
		absErr, 100*float64(absErr)/float64(sum(exact)))

	fmt.Printf("\nadmission table: %d entries, %d/400 bytes used\n", table.Len(), table.Used())
	for _, e := range table.Entries() {
		fmt.Printf("  %-10s freq>=%-6d bytes=%d\n", e.Key, e.Freq, e.Bytes)
	}
}

func sum(m map[string]uint64) uint64 {
	var s uint64
	for _, v := range m {
		s += v
	}
	return s
}
