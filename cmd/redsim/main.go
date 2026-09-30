// redsim 是 RED 式早丢弃的离散事件模拟样例：整数 tick、可注入随机源、
// 多队列共享链路 + DRR 公平调度，不发真实网络流量。
package main

import (
	"flag"
	"fmt"

	"redsim/redsim"
)

func buildArrivals(nQueues, perQueue int, period int64, sizes []int) []redsim.Arrival {
	var arrivals []redsim.Arrival
	var id uint64
	for q := 0; q < nQueues; q++ {
		for i := 0; i < perQueue; i++ {
			arrivals = append(arrivals, redsim.Arrival{
				Tick: int64(i)*period + int64(q), // 队列间错相
				Pkt:  redsim.Packet{ID: id, Queue: q, Len: sizes[q%len(sizes)]},
			})
			id++
		}
	}
	return arrivals
}

func runScenario(name string, seed uint64, cfg redsim.REDConfig, end int64, verbose bool) []*redsim.Queue {
	const nQueues = 3
	queues := make([]*redsim.Queue, nQueues)
	for i := range queues {
		queues[i] = redsim.NewQueue(i, cfg)
	}
	link := redsim.NewLink(380, 1500, nQueues) // 380 字节/tick，quantum=MTU
	// 到达贯穿整个仿真：三队列合计约 520 字节/tick，轻度过载，
	// 使平均队长在概率丢包区间内波动。
	arrivals := buildArrivals(nQueues, int(end)/5, 5, []int{1500, 800, 300})
	sim := redsim.NewSim(seed, queues, link, arrivals)
	sim.Run(end)

	fmt.Printf("== %s (seed=%d) ==\n", name, seed)
	if verbose {
		fmt.Println("-- 每次丢弃依据 --")
		for _, d := range sim.Drops {
			fmt.Printf("tick=%-6d q=%d evt=%-4d len=%-5d avg=%8.3f idleTicks=%-3d pb=%.4f pa=%.4f draw=%.4f -> %s\n",
				d.Tick, d.Queue, d.EventID, d.PktLen, d.Avg, d.IdleTicks, d.Pb, d.Pa, d.Draw, d.Reason)
		}
	}
	for _, q := range queues {
		st := q.Stats
		rate := 0.0
		if st.Arrived > 0 {
			rate = float64(st.Dropped) / float64(st.Arrived)
		}
		fmt.Printf("q%d: arrived=%d dropped=%d departed=%d dropRate=%.4f bytesIn=%d bytesOut=%d bytesDropped=%d\n",
			q.ID, st.Arrived, st.Dropped, st.Departed, rate, st.BytesArrived, st.BytesOut, st.BytesDropped)
	}
	fmt.Printf("byteConservation=%v\n", sim.CheckByteConservation())

	// 性能对照只看分窗口吞吐，不用平均延迟掩盖长期饥饿。
	windows := sim.WindowedBytes(2000)
	fmt.Println("-- 分窗口离链字节（窗口=2000 tick，前 5 窗）--")
	for q := range windows {
		fmt.Printf("q%d:", q)
		for w := 0; w < 5 && w < len(windows[q]); w++ {
			fmt.Printf(" %d", windows[q][w])
		}
		fmt.Println()
	}
	fmt.Println()
	return queues
}

func main() {
	seed := flag.Uint64("seed", 42, "主随机种子")
	end := flag.Int64("ticks", 10000, "模拟时长（整数 tick）")
	flag.Parse()

	base := redsim.REDConfig{MinTh: 3, MaxTh: 12, Wq: 0.002, MaxP: 0.1, MTU: 1500}
	runScenario("包模式 RED + DRR", *seed, base, *end, true)

	byteMode := base
	byteMode.ByteMode = true
	runScenario("字节模式 RED（包长归一化）+ DRR", *seed, byteMode, *end, false)
}
