// Command redsim runs a RED + DRR discrete-event simulation entirely in
// memory (integer ticks, no real network traffic) and prints per-flow
// performance counters, the basis of sampled drops, and a reproducibility
// check. It is a performance comparison harness: fairness is judged by
// per-flow throughput and worst-case delay, not by mean delay alone.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"

	"redsim/red"
	"redsim/sim"
)

type arrival struct {
	tick uint64
	flow int
	size int
}

// genTraffic builds a deterministic arrival stream from a keyed source, so
// traffic generation is reproducible and independent of the RED seed.
func genTraffic(seed uint64, flows int, horizon uint64) []arrival {
	src := red.KeyedSource{Seed: seed}
	var out []arrival
	for f := 0; f < flows; f++ {
		tick := uint64(0)
		for k := uint64(0); ; k++ {
			gap := uint64(8 + src.Uint64(uint64(f)<<40|k*2)%30)
			size := int(64 + src.Uint64(uint64(f)<<40|k*2+1)%1437)
			tick += gap
			if tick > horizon {
				break
			}
			out = append(out, arrival{tick, f, size})
		}
	}
	return out
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func jain(xs []uint64) float64 {
	var sum, sq float64
	for _, x := range xs {
		sum += float64(x)
		sq += float64(x) * float64(x)
	}
	if sq == 0 {
		return 1
	}
	return sum * sum / (float64(len(xs)) * sq)
}

func main() {
	seed := flag.Uint64("seed", 42, "master seed for RED sampling")
	flows := flag.Int("flows", 3, "number of flows / RED queues")
	horizon := flag.Uint64("ticks", 30000, "arrival horizon in integer ticks")
	rate := flag.Int("rate", 100, "link rate in bytes per tick")
	quantum := flag.Int("quantum", 1500, "DRR quantum in bytes")
	dropSamples := flag.Int("drop-samples", 8, "number of drop bases to print")
	flag.Parse()

	params := make([]red.Params, *flows)
	for i := range params {
		params[i] = red.Params{
			MinThBytes:     4000,
			MaxThBytes:     12000,
			MaxProb:        0.5,
			Weight:         0.002,
			CapacityBytes:  30000,
			MaxPacketBytes: 1500,
		}
	}
	cfg := sim.Config{LinkRateBytesPerTick: *rate, QuantumBytes: *quantum}
	arrivals := genTraffic(*seed+1, *flows, *horizon)

	run := func() *sim.Network {
		n, err := sim.New(cfg, params, *seed)
		check(err)
		for _, a := range arrivals {
			check(n.AddArrival(a.tick, a.flow, a.size))
		}
		n.Run(*horizon)
		for n.Pending() {
			n.Run(n.Now() + 100000)
		}
		return n
	}
	n1 := run()
	n2 := run() // replay with the same seed

	fmt.Println("RED discrete-event simulation (integer ticks, in-memory only)")
	fmt.Printf("seed=%d flows=%d horizon=%d link=%dB/tick quantum=%dB\n",
		*seed, *flows, *horizon, *rate, *quantum)
	fmt.Printf("red: minTh=%.0fB maxTh=%.0fB maxP=%.2f wq=%.3f capacity=%dB maxPkt=%dB\n\n",
		params[0].MinThBytes, params[0].MaxThBytes, params[0].MaxProb,
		params[0].Weight, params[0].CapacityBytes, params[0].MaxPacketBytes)

	stats := n1.Stats()
	fmt.Println("flow  arrived  completed  dropped  drop%   bytesIn   bytesOut  avgDelay  maxDelay")
	completed := make([]uint64, len(stats))
	var totArr, totDrop uint64
	for f, s := range stats {
		completed[f] = s.BytesCompleted
		totArr += s.Arrived
		totDrop += s.Dropped
		dropPct := 0.0
		if s.Arrived > 0 {
			dropPct = 100 * float64(s.Dropped) / float64(s.Arrived)
		}
		fmt.Printf("%4d  %7d  %9d  %7d  %5.1f  %8d  %8d  %8.1f  %8d\n",
			f, s.Arrived, s.Completed, s.Dropped, dropPct,
			s.BytesArrived, s.BytesCompleted, s.MeanDelay(), s.MaxDelay)
	}

	fmt.Printf("\ntotals: arrived=%d dropped=%d (%.1f%%)\n",
		totArr, totDrop, 100*float64(totDrop)/float64(totArr))
	minB, maxB := uint64(math.MaxUint64), uint64(0)
	for _, b := range completed {
		if b < minB {
			minB = b
		}
		if b > maxB {
			maxB = b
		}
	}
	fmt.Printf("fairness: Jain index=%.4f  min/max flow throughput=%.4f\n",
		jain(completed), float64(minB)/float64(maxB))
	var worstMax uint64
	for _, s := range stats {
		if s.MaxDelay > worstMax {
			worstMax = s.MaxDelay
		}
	}
	fmt.Printf("worst-case delay across flows: %d ticks (reported so averages cannot hide starvation)\n", worstMax)

	counts := map[red.Verdict]int{}
	for _, d := range n1.Drops {
		counts[d.Decision.Verdict]++
	}
	fmt.Printf("\ndrop verdicts: probabilistic=%d forced=%d overflow=%d (total=%d)\n",
		counts[red.DropProbabilistic], counts[red.DropForced], counts[red.DropOverflow], len(n1.Drops))

	fmt.Println("sample drop bases:")
	for i, d := range n1.Drops {
		if i >= *dropSamples {
			fmt.Printf("  ... (%d more drops omitted)\n", len(n1.Drops)-*dropSamples)
			break
		}
		fmt.Printf("  tick=%-6d flow=%d pkt=%-5d size=%-4dB verdict=%s avg=%.1f p=%.4f draw=%.4f\n",
			d.Tick, d.Flow, d.Packet, d.Size, d.Decision.Verdict,
			d.Decision.Avg, d.Decision.Prob, d.Decision.Draw)
		fmt.Printf("    basis: %s\n", d.Decision.Reason)
	}

	fmt.Printf("\nreproducibility: run1 digest=%016x run2 digest=%016x identical=%v\n",
		n1.Digest(), n2.Digest(), n1.Digest() == n2.Digest())
}
