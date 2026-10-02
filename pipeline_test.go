package vecpool

import (
	"math"
	"testing"
)

// buildTenOpPipeline 构建包含 10 个复杂算子的查询流水线。
func buildTenOpPipeline(pool *Pool) *Pipeline {
	return NewPipeline(pool,
		&AddScalar{K: 3.5},
		&MulScalar{K: 2},
		&SubScalar{K: 1.25},
		&DivScalar{K: 4},
		&AbsOp{},
		&NegOp{},
		&SqrtOp{},
		&SquareOp{},
		&ClampOp{Lo: 0, Hi: 1e6},
		&ScaleShiftOp{Scale: 0.5, Shift: -7},
	)
}

// expected 用标量方式复算 10 个算子的期望结果。
func expected(v float64) float64 {
	v = v + 3.5
	v = v * 2
	v = v - 1.25
	v = v / 4
	v = math.Abs(v)
	v = -v
	v = math.Sqrt(math.Abs(v))
	v = v * v
	if v < 0 {
		v = 0
	} else if v > 1e6 {
		v = 1e6
	}
	v = v*0.5 - 7
	return v
}

func fillChunk(c *Chunk) {
	c.N = ChunkSize
	for i := 0; i < c.N; i++ {
		c.Data[i] = float64(i)*0.25 - 128.0
	}
}

// TestPipelineCorrectness 验证 10 算子流水线的计算正确性。
func TestPipelineCorrectness(t *testing.T) {
	pool := NewPool(4)
	pl := buildTenOpPipeline(pool)
	if pl.OpCount() != 10 {
		t.Fatalf("op count = %d, want 10", pl.OpCount())
	}
	src := pool.Get()
	fillChunk(src)
	out := pl.Execute(src)
	if out == nil {
		t.Fatal("pipeline returned nil")
	}
	for i := 0; i < out.N; i++ {
		want := expected(float64(i)*0.25 - 128.0)
		if math.Abs(out.Data[i]-want) > 1e-9 {
			t.Fatalf("row %d: got %v, want %v", i, out.Data[i], want)
		}
	}
	pool.Put(out)
	st := pool.Stats()
	if st.InUse != 0 {
		t.Fatalf("chunk leak: %d chunks still in use", st.InUse)
	}
	if st.Gets != st.Puts {
		t.Fatalf("gets(%d) != puts(%d)", st.Gets, st.Puts)
	}
}

// TestZeroAllocation 验收核心：流水线建立后连续执行，
// 堆分配计数为零、无 GC 停顿、池不发生任何新分配。
func TestZeroAllocation(t *testing.T) {
	const iterations = 10000

	pool := NewPool(4)
	pl := buildTenOpPipeline(pool)
	src := pool.Get()
	fillChunk(src)

	// 预热内存监控探针，排除运行时一次性初始化分配的干扰。
	Warmup()

	// 预热：让流水线进入稳态（避开首次执行的边界效应）。
	for i := 0; i < 100; i++ {
		if pl.Execute(src) == nil {
			t.Fatal("warmup: pipeline returned nil")
		}
	}

	start := SampleHeap()
	for i := 0; i < iterations; i++ {
		if pl.Execute(src) == nil {
			t.Fatal("pipeline returned nil")
		}
	}
	end := SampleHeap()
	d := Diff(start, end)

	t.Logf("iterations=%d mallocs=%d heapDelta=%dB gcRuns=%d gcPause=%dns",
		iterations, d.Mallocs, d.HeapAlloc, d.NumGC, d.PauseNs)

	if d.Mallocs != 0 {
		t.Fatalf("heap allocations during execution: %d, want 0", d.Mallocs)
	}
	if d.NumGC != 0 {
		t.Fatalf("GC triggered during execution: %d times, want 0", d.NumGC)
	}
	if d.PauseNs != 0 {
		t.Fatalf("GC pause during execution: %dns, want 0", d.PauseNs)
	}

	st := pool.Stats()
	if st.Allocs != 0 {
		t.Fatalf("pool heap allocs: %d, want 0", st.Allocs)
	}
	if st.Misses != 0 {
		t.Fatalf("pool exhausted: %d misses", st.Misses)
	}
	if st.Gets != st.Puts+1 { // src 仍被测试持有
		t.Fatalf("chunk leak: gets=%d puts=%d", st.Gets, st.Puts)
	}
	pool.Put(src)

	// 权威复核：标准库 AllocsPerRun 测得的单次执行分配数必须为 0。
	if a := testing.AllocsPerRun(1000, func() { pl.Execute(src) }); a != 0 {
		t.Fatalf("AllocsPerRun = %v, want 0", a)
	}
}

// TestChunkReuse 验证下游确实复用了归还的内存槽位：
// 相邻两个算子使用的输出 Chunk 应当来自同一槽位循环。
func TestChunkReuse(t *testing.T) {
	// 容量 3 = 调用方持有的源块 + 流水线一进一出两个在途块。
	pool := NewPool(3)
	pl := NewPipeline(pool,
		&AddScalar{K: 1},
		&MulScalar{K: 2},
		&AddScalar{K: 3},
	)
	src := pool.Get()
	fillChunk(src)
	if pl.Execute(src) == nil {
		t.Fatal("pipeline returned nil")
	}
	st := pool.Stats()
	// 源块借出 1 次 + 3 个算子各借出 1 次，中间块全部归还；
	// 池中只有 3 个槽位，gets=4 > capacity=3 证明发生了槽位复用。
	if st.Gets != 4 || st.Puts != 3 {
		t.Fatalf("gets=%d puts=%d, want 4/3", st.Gets, st.Puts)
	}
	if st.Misses != 0 {
		t.Fatalf("pool misses: %d", st.Misses)
	}
	pool.Put(src)
}

// TestPoolExhaustionNoAlloc 验证池耗尽时返回 nil 而非偷偷分配堆内存。
func TestPoolExhaustionNoAlloc(t *testing.T) {
	pool := NewPool(1)
	a := pool.Get()
	if a == nil {
		t.Fatal("first Get returned nil")
	}
	b := pool.Get()
	if b != nil {
		t.Fatal("second Get should return nil when pool is exhausted")
	}
	if pool.Stats().Misses != 1 {
		t.Fatal("miss not recorded")
	}
	pool.Put(a)
	if pool.Get() == nil {
		t.Fatal("Get after Put should succeed")
	}
}

// BenchmarkPipeline 基准测试：allocs/op 必须为 0。
func BenchmarkPipeline(b *testing.B) {
	pool := NewPool(4)
	pl := buildTenOpPipeline(pool)
	src := pool.Get()
	fillChunk(src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pl.Execute(src)
	}
}
