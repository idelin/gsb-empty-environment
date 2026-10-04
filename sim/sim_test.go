package sim

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
)

// Golden checksums for seed=42, 8 bitmaps. Any behavioral change in the
// bitmap engine or the simulator breaks this regression test. The race
// detector slows the run ~100x, so it uses a shorter (still deterministic)
// schedule with its own golden value.
const goldenChecksum1M uint64 = 13495112714683366617
const goldenChecksum100k uint64 = 11262049745226334667

func TestDeterministicMillionSteps(t *testing.T) {
	old := debug.SetGCPercent(500)
	defer debug.SetGCPercent(old)

	steps := uint64(1000000)
	golden := goldenChecksum1M
	if raceEnabled {
		steps = 100000
		golden = goldenChecksum100k
	}
	e := NewEngine(42, 8)
	sum := e.Run(steps, nil)
	if sum != golden {
		t.Fatalf("regression: checksum = %d, want %d", sum, golden)
	}
	if e.Steps() != steps {
		t.Fatalf("steps = %d", e.Steps())
	}
}

func TestDeterminismRepeatable(t *testing.T) {
	sums := map[int64]uint64{}
	for _, seed := range []int64{1, 2, 3} {
		a := NewEngine(seed, 8).Run(50000, nil)
		b := NewEngine(seed, 8).Run(50000, nil)
		if a != b {
			t.Fatalf("seed %d not deterministic: %d vs %d", seed, a, b)
		}
		sums[seed] = a
	}
	if sums[1] == sums[2] || sums[2] == sums[3] || sums[1] == sums[3] {
		t.Fatal("different seeds produced identical checksums")
	}
}

func TestEventScheduleDeterministic(t *testing.T) {
	collect := func() []uint64 {
		e := NewEngine(99, 4)
		var times []uint64
		for i := 0; i < 1000; i++ {
			e.Step()
			times = append(times, e.Now)
		}
		return times
	}
	a, b := collect(), collect()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("schedule diverged at event %d: %d vs %d", i, a[i], b[i])
		}
		if i > 0 && a[i] < a[i-1] {
			t.Fatalf("clock moved backwards at event %d", i)
		}
	}
}

func TestRecordAndReplay(t *testing.T) {
	var buf bytes.Buffer
	rec := NewRecorder(&buf)
	e := NewEngine(42, 8)
	final := e.Run(50000, rec.Hook())
	rec.Flush()

	if len(rec.Records) != 50000 {
		t.Fatalf("recorded %d records", len(rec.Records))
	}
	// in-memory replay
	if f := Replay(42, 8, rec.Records); f != nil {
		t.Fatalf("replay diverged: %v", f)
	}
	// text-log round trip
	recs, err := ParseLog(&buf)
	if err != nil {
		t.Fatalf("ParseLog: %v", err)
	}
	if len(recs) != 50000 {
		t.Fatalf("parsed %d records", len(recs))
	}
	if f := Replay(42, 8, recs); f != nil {
		t.Fatalf("log replay diverged: %v", f)
	}
	if recs[len(recs)-1].Sum != final {
		t.Fatal("final checksum mismatch between engine and log")
	}
}

func TestReplayDetectsFault(t *testing.T) {
	rec := NewRecorder(nil)
	e := NewEngine(7, 8)
	e.Run(20000, rec.Hook())

	// corrupt one op in the middle: extend a range end
	victim := -1
	for i, r := range rec.Records {
		if i > 100 && r.Op.Kind == OpAddRange && r.Op.End < (1<<18) {
			victim = i
			break
		}
	}
	if victim < 0 {
		t.Fatal("no ADD_RANGE victim found")
	}
	rec.Records[victim].Op.End += 1

	f := Replay(7, 8, rec.Records)
	if f == nil {
		t.Fatal("corrupted log was not detected")
	}
	if f.Step != rec.Records[victim].Step {
		t.Fatalf("fault reported at step %d, want %d", f.Step, rec.Records[victim].Step)
	}

	repro := Reproduce(7, 8, rec.Records, f)
	if len(repro) == 0 {
		t.Fatal("empty reproduction")
	}
	// the reproduction must itself replay to the same fault
	recs, err := ParseLog(bytes.NewBufferString(repro))
	if err != nil {
		t.Fatalf("repro log unparseable: %v", err)
	}
	f2 := Replay(7, 8, recs)
	if f2 == nil || f2.Step != f.Step {
		t.Fatalf("repro did not reproduce fault: %+v", f2)
	}
}

func TestReplayDetectsChecksumTampering(t *testing.T) {
	rec := NewRecorder(nil)
	e := NewEngine(13, 8)
	e.Run(5000, rec.Hook())
	rec.Records[1234].Sum ^= 0xdeadbeef
	f := Replay(13, 8, rec.Records)
	if f == nil {
		t.Fatal("tampered checksum not detected")
	}
	if f.Step != rec.Records[1234].Step {
		t.Fatalf("fault at step %d, want %d", f.Step, rec.Records[1234].Step)
	}
}

func TestConcurrentEnginesDeterministic(t *testing.T) {
	before := runtime.NumGoroutine()
	const want = uint64(0)
	results := make([]uint64, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			results[g] = NewEngine(42, 8).Run(20000, nil)
		}(g)
	}
	wg.Wait()
	for i, s := range results {
		if i > 0 && s != results[0] {
			t.Fatalf("engine %d diverged under concurrency: %d vs %d", i, s, results[0])
		}
		if s == want {
			t.Fatal("zero checksum")
		}
	}
	for i := 0; i < 100 && runtime.NumGoroutine() > before; i++ {
		runtime.Gosched()
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

func TestFaultErrorMessage(t *testing.T) {
	f := &Fault{Step: 10, Op: Op{Kind: OpAddRange, Target: 1, Start: 5, End: 9}, Expected: 1, Actual: 2}
	msg := f.Error()
	if msg == "" || !bytes.Contains([]byte(msg), []byte("step 10")) {
		t.Fatalf("bad fault message: %q", msg)
	}
}

func ExampleEngine_Run() {
	e := NewEngine(42, 8)
	sum := e.Run(1000, nil)
	fmt.Println(sum > 0)
	// Output: true
}
