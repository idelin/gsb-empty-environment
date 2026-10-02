// demo 运行样例：构建含 10 个复杂算子的查询流水线，
// 连续执行 100000 轮，实时监控堆分配、GC 停顿与内存曲线。
package main

import (
	"fmt"

	"vecpool"
)

func main() {
	const (
		poolCapacity = 4
		warmup       = 1000
		iterations   = 100000
		reportEvery  = 20000
	)

	pool := vecpool.NewPool(poolCapacity)

	// 10 个复杂算子组成的查询流水线。
	pipeline := vecpool.NewPipeline(pool,
		&vecpool.AddScalar{K: 3.5},
		&vecpool.MulScalar{K: 2},
		&vecpool.SubScalar{K: 1.25},
		&vecpool.DivScalar{K: 4},
		&vecpool.AbsOp{},
		&vecpool.NegOp{},
		&vecpool.SqrtOp{},
		&vecpool.SquareOp{},
		&vecpool.ClampOp{Lo: 0, Hi: 1e6},
		&vecpool.ScaleShiftOp{Scale: 0.5, Shift: -7},
	)

	src := pool.Get()
	src.N = vecpool.ChunkSize
	for i := 0; i < src.N; i++ {
		src.Data[i] = float64(i)*0.25 - 128.0
	}

	fmt.Printf("pipeline: %d operators, chunk=%d floats, pool capacity=%d\n",
		pipeline.OpCount(), vecpool.ChunkSize, poolCapacity)

	// 预热内存监控探针（排除运行时一次性初始化分配的干扰）。
	vecpool.Warmup()

	// 预热，使流水线进入稳态。
	for i := 0; i < warmup; i++ {
		pipeline.Execute(src)
	}
	fmt.Printf("warmup done (%d rounds), entering zero-allocation steady state\n\n", warmup)

	base := vecpool.SampleHeap()
	const reports = iterations / reportEvery
	var samples [reports]vecpool.MemSample
	// 测量区间内不做任何打印（fmt.Printf 自身会分配），
	// 采样结果先存入预分配数组，循环结束后再统一输出。
	for i := 1; i <= iterations; i++ {
		if pipeline.Execute(src) == nil {
			panic("pool exhausted")
		}
		if i%reportEvery == 0 {
			samples[i/reportEvery-1] = vecpool.SampleHeap()
		}
	}
	end := vecpool.SampleHeap()

	fmt.Printf("%-10s %-14s %-14s %-10s %-12s\n",
		"round", "mallocs(+)", "heapAlloc(B)", "gcRuns(+)", "gcPause(ns)")
	prev := base
	for i := 0; i < reports; i++ {
		d := vecpool.Diff(prev, samples[i])
		total := vecpool.Diff(base, samples[i])
		fmt.Printf("%-10d %-14d %-14d %-10d %-12d\n",
			(i+1)*reportEvery, d.Mallocs, samples[i].HeapAlloc, total.NumGC, total.PauseNs)
		prev = samples[i]
	}
	total := vecpool.Diff(base, end)
	st := pool.Stats()

	fmt.Println("\n---- steady-state summary ----")
	fmt.Printf("iterations          : %d\n", iterations)
	fmt.Printf("heap mallocs        : %d\n", total.Mallocs)
	fmt.Printf("heap alloc delta    : %+d bytes\n", total.HeapAlloc)
	fmt.Printf("GC runs             : %d\n", total.NumGC)
	fmt.Printf("GC pause total      : %d ns\n", total.PauseNs)
	fmt.Printf("pool gets/puts      : %d/%d\n", st.Gets, st.Puts)
	fmt.Printf("pool heap allocs    : %d\n", st.Allocs)
	fmt.Printf("pool misses         : %d\n", st.Misses)
	fmt.Printf("sample result[0..3] : %.4f %.4f %.4f %.4f\n",
		src.Data[0], src.Data[1], src.Data[2], src.Data[3])

	if total.Mallocs == 0 && total.NumGC == 0 && st.Allocs == 0 && st.Misses == 0 {
		fmt.Println("\nPASS: zero heap allocation, zero GC pause, flat memory curve.")
	} else {
		fmt.Println("\nFAIL: allocation or GC activity detected.")
	}
	pool.Put(src)
}
