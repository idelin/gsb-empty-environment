// pktscale-demo: scale two record streams by rational time/byte rates,
// merge them, run them over a simulated link, and audit the result with
// the independent reference checker. No real network traffic is used.
package main

import (
	"fmt"
	"math/big"

	"pktscale"
)

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

func main() {
	// 输入：两条流，间隔 3 tick、包长 800 字节。
	var recs []pktscale.Record
	for i := 0; i < 12; i++ {
		recs = append(recs, pktscale.Record{
			Seq:    int64(i),
			Stream: i % 2,
			Time:   int64(i/2) * 3,
			Size:   800,
		})
	}

	// 时间倍率 2/3（压缩），字节倍率 5/4（放大 -> 超 MTU 触发分片）。
	sc, err := pktscale.NewScaler(rat(2, 3), rat(5, 4), 600, 1)
	if err != nil {
		panic(err)
	}
	var batches [][]pktscale.Fragment
	for _, r := range recs {
		f, err := sc.Process(r)
		if err != nil {
			panic(err)
		}
		batches = append(batches, f)
	}
	merged := pktscale.Merge(batches...)

	fmt.Println("== scaled + merged fragments (tick stream seq frag size) ==")
	for _, f := range merged {
		fmt.Printf("tick %3d  stream %d  seq %2d  frag %d/%d  size %4d\n",
			f.Tick, f.Stream, f.Seq, f.FragID+1, f.FragCnt, f.Size)
	}

	// 离散事件仿真：150 B/tick、时延 2 tick 的链路。
	link := &pktscale.Link{Bandwidth: 150, Delay: 2}
	var sim pktscale.Sim
	for _, f := range merged {
		sim.Submit(link, f)
	}
	sim.Run()
	fmt.Println("\n== link arrivals (first 8) ==")
	for i, a := range sim.Arrivals {
		if i >= 8 {
			fmt.Printf("... (%d more)\n", len(sim.Arrivals)-8)
			break
		}
		fmt.Printf("arrive tick %3d  stream %d  seq %2d  frag %d\n",
			a.Tick, a.Frag.Stream, a.Frag.Seq, a.Frag.FragID+1)
	}

	// 独立精确时间轴审计。
	segs := []pktscale.RateSegment{{At: 0, Rate: rat(2, 3)}}
	drift := pktscale.CheckDrift(segs, recs, batches, 1)
	fmt.Printf("\ndrift bound check (<=1 tick): %d violation(s)\n", len(drift))
	last := batches[len(batches)-1][0].Tick
	exact := pktscale.ExactTimeline(segs, recs[len(recs)-1].Time)
	fmt.Printf("last packet: emitted tick %d, exact position %s\n", last, exact.RatString())

	spacing := pktscale.CheckSpacing(batches, 1, link.Bandwidth)
	fmt.Printf("spacing check (min gap 1, link 150 B/tick): %d violation(s)\n", len(spacing))
	for _, v := range spacing {
		fmt.Println("  !", v)
	}
}
