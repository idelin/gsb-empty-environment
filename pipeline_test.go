package vecpool

import (
	"math"
	"testing"
)

// newTestPipeline builds the same 10-operator complex query used by the
// acceptance scenario.
func newTestPipeline(pool *Pool) *Pipeline {
	return NewPipeline(pool,
		AddScalar(500),
		MulScalar(1.5),
		Square(),
		Sqrt(),
		FilterGreaterThan{Threshold: 10},
		Log1pAbs(),
		Sin(),
		Abs(),
		SubtractMean(),
		Clamp(0, 100),
	)
}

// reference applies the same 10 operators to a plain slice, element by
// element, and is used as the ground truth for the vectorized pipeline.
func reference(input []float64) []float64 {
	v := make([]float64, len(input))
	copy(v, input)
	for i := range v {
		v[i] += 500
	}
	for i := range v {
		v[i] *= 1.5
	}
	for i := range v {
		v[i] *= v[i]
	}
	for i := range v {
		v[i] = math.Sqrt(v[i])
	}
	kept := v[:0]
	for _, x := range v {
		if x > 10 {
			kept = append(kept, x)
		}
	}
	v = kept
	for i := range v {
		v[i] = math.Log1p(math.Abs(v[i]))
	}
	for i := range v {
		v[i] = math.Sin(v[i])
	}
	for i := range v {
		v[i] = math.Abs(v[i])
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	mean := sum / float64(len(v))
	for i := range v {
		v[i] -= mean
	}
	for i := range v {
		if v[i] < 0 {
			v[i] = 0
		} else if v[i] > 100 {
			v[i] = 100
		}
	}
	return v
}

func fillChunk(c *Chunk, seed int) {
	for j := range c.Data {
		c.Data[j] = float64((seed+j)%2000) - 1000.0
	}
	c.Len = ChunkSize
}

func TestPipelineHasTenOperators(t *testing.T) {
	pipe := newTestPipeline(NewPool(4))
	if pipe.Len() != 10 {
		t.Fatalf("pipeline has %d operators, want 10", pipe.Len())
	}
}

func TestPipelineCorrectness(t *testing.T) {
	pool := NewPool(4)
	pipe := newTestPipeline(pool)

	in := pool.Get()
	fillChunk(in, 7)
	want := reference(in.Data[:in.Len])

	out := pipe.Execute(in)
	defer pool.Put(out)

	if out.Len != len(want) {
		t.Fatalf("output length = %d, want %d", out.Len, len(want))
	}
	for i, w := range want {
		if got := out.Data[i]; got != w {
			t.Fatalf("element %d = %v, want %v", i, got, w)
		}
	}
}

func TestFilterCompaction(t *testing.T) {
	pool := NewPool(2)
	in := pool.Get()
	vals := []float64{-5, 3, 0, 42, -1, 11}
	copy(in.Data, vals)
	in.Len = len(vals)

	out := (FilterGreaterThan{Threshold: 10}).Process(in, pool)
	defer pool.Put(out)
	if out.Len != 2 || out.Data[0] != 42 || out.Data[1] != 11 {
		t.Fatalf("filtered chunk = %v (len %d), want [42 11]", out.Data[:out.Len], out.Len)
	}
}

// TestZeroAllocationDuringExecution is the core acceptance check: after the
// pipeline is established, executing the full 10-operator query and
// returning the result to the pool performs zero heap allocations.
func TestZeroAllocationDuringExecution(t *testing.T) {
	pool := NewPool(4)
	pipe := newTestPipeline(pool)

	allocs := testing.AllocsPerRun(200, func() {
		in := pool.Get()
		fillChunk(in, 13)
		out := pipe.Execute(in)
		pool.Put(out)
	})
	if allocs != 0 {
		t.Fatalf("pipeline execution allocated %v times per run, want 0", allocs)
	}
}

func TestPoolGetPutZeroAllocation(t *testing.T) {
	pool := NewPool(2)
	allocs := testing.AllocsPerRun(200, func() {
		c := pool.Get()
		pool.Put(c)
	})
	if allocs != 0 {
		t.Fatalf("pool Get/Put allocated %v times per run, want 0", allocs)
	}
}

func TestChunkMemoryReuse(t *testing.T) {
	pool := NewPool(1)
	first := pool.Get()
	backing := &first.Data[0]
	pool.Put(first)

	second := pool.Get()
	if second != first {
		t.Fatal("pool did not hand back the returned chunk")
	}
	if &second.Data[0] != backing {
		t.Fatal("reused chunk does not share the original backing array")
	}
	pool.Put(second)
}

// TestMonitorFlatMemoryCurve runs the acceptance scenario end to end: a
// stream of chunks through the 10-operator pipeline while the memory monitor
// samples the heap. The measurement runs under testing.AllocsPerRun, which
// pins GOMAXPROCS=1 so the heap counters reflect only this goroutine: the
// allocation count must be exactly zero, the memory curve flat, and no GC
// cycle or STW pause may occur.
func TestMonitorFlatMemoryCurve(t *testing.T) {
	pool := NewPool(8)
	pipe := newTestPipeline(pool)

	const runs = 50
	const chunksPerRun = 1000
	mon := NewMemMonitor(2*(runs+1) + 8)

	allocs := testing.AllocsPerRun(runs, func() {
		mon.Sample() // baseline sample for this run
		for i := 0; i < chunksPerRun; i++ {
			in := pool.Get()
			fillChunk(in, i)
			out := pipe.Execute(in)
			pool.Put(out)
		}
		mon.Sample()
	})
	if allocs != 0 {
		t.Fatalf("pipeline allocated %v times per run, want 0", allocs)
	}

	allocBytes, gcCycles, pauseNs := mon.Delta()
	if allocBytes != 0 {
		t.Fatalf("heap allocated %d bytes during query execution, want 0", allocBytes)
	}
	if gcCycles != 0 {
		t.Fatalf("%d GC cycles ran during query execution, want 0", gcCycles)
	}
	if pauseNs != 0 {
		t.Fatalf("GC STW pause of %d ns during query execution, want 0", pauseNs)
	}
	t.Logf("flat memory curve confirmed over %d chunks: alloc=%d B, GC=%d, pause=%d ns",
		runs*chunksPerRun, allocBytes, gcCycles, pauseNs)
}

func TestPoolExhaustionBlocks(t *testing.T) {
	pool := NewPool(1)
	c := pool.Get()
	released := make(chan struct{})
	go func() {
		pool.Put(c)
		close(released)
	}()
	got := pool.Get() // must block until the Put above
	<-released
	if got != c {
		t.Fatal("expected to reuse the released chunk")
	}
	pool.Put(got)
}
