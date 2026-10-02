package sim

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// chainWorkload schedules a self-perpetuating event chain whose state
// evolves deterministically from the engine's seeded random stream.
func chainWorkload(e *Engine, emit Emit) {
	var state uint64
	var tick EventFunc
	tick = func(e *Engine) {
		state = state*6364136223846793005 + e.NextUint64() + 1442695040888963407
		emit("tick", state)
		e.Schedule(uint64(e.Intn(7)+1), 0, "tick", tick)
	}
	e.Schedule(0, 0, "tick", tick)
}

// TestDeterministicRegression runs one million events and pins the
// trace hash: any change to scheduling, RNG, or ordering breaks it.
func TestDeterministicRegression(t *testing.T) {
	const steps = 1_000_000
	res := RunWorkloadNoTrace(20261002, steps, chainWorkload, nil)
	if res.Steps != steps {
		t.Fatalf("steps = %d, want %d", res.Steps, steps)
	}
	const golden = uint64(0x9fd9b4eaa7d0c5c8)
	if res.Hash != golden {
		t.Fatalf("regression hash = %016x, want %016x", res.Hash, golden)
	}
	// Same seed, second run: bit-for-bit identical.
	again := RunWorkloadNoTrace(20261002, steps, chainWorkload, nil)
	if again.Hash != res.Hash {
		t.Fatalf("non-deterministic: %016x vs %016x", again.Hash, res.Hash)
	}
	// Different seed must diverge.
	other := RunWorkloadNoTrace(1, steps, chainWorkload, nil)
	if other.Hash == res.Hash {
		t.Fatal("different seeds produced identical traces")
	}
}

func TestEventOrdering(t *testing.T) {
	e := New(7)
	var got []string
	emit := func(tag string, _ uint64) { got = append(got, tag) }
	// Same timestamp: priority first, then insertion sequence.
	e.Schedule(5, 2, "", func(e *Engine) { emit("p2-first", 0) })
	e.Schedule(5, 0, "", func(e *Engine) { emit("p0", 0) })
	e.Schedule(5, 2, "", func(e *Engine) { emit("p2-second", 0) })
	e.Schedule(1, 9, "", func(e *Engine) { emit("early", 0) })
	e.RunSteps(4)
	want := []string{"early", "p0", "p2-first", "p2-second"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

func TestRunUntil(t *testing.T) {
	e := New(1)
	var times []uint64
	for i := 0; i < 10; i++ {
		e.Schedule(uint64(i*10), 0, "", func(e *Engine) { times = append(times, e.Now()) })
	}
	e.Run(45)
	if len(times) != 5 || e.Now() != 40 {
		t.Fatalf("times=%v now=%d", times, e.Now())
	}
}

func TestRNGDeterminism(t *testing.T) {
	a, b := NewRNG(99), NewRNG(99)
	for i := 0; i < 1000; i++ {
		if a.Next() != b.Next() {
			t.Fatalf("diverged at %d", i)
		}
	}
	c := NewRNG(100)
	if c.Next() == NewRNG(101).Next() {
		t.Fatal("adjacent seeds collided")
	}
}

// TestConcurrentSchedule hammers Schedule from many goroutines while
// the engine runs, and asserts no goroutine leaks afterwards.
func TestConcurrentSchedule(t *testing.T) {
	before := runtime.NumGoroutine()
	e := New(3)
	const producers = 8
	const perProducer = 2000
	var wg sync.WaitGroup
	var counter uint64
	var mu sync.Mutex
	for g := 0; g < producers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				e.Schedule(uint64(i%16), g, "ext", func(e *Engine) {
					mu.Lock()
					counter++
					mu.Unlock()
				})
			}
		}(g)
	}
	wg.Wait()
	e.RunSteps(producers * perProducer)
	if counter != producers*perProducer {
		t.Fatalf("executed %d events, want %d", counter, producers*perProducer)
	}
	// Leak check: allow the scheduler a moment to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

// buggyWorkload corrupts its state at step bugAt (state becomes the
// poison marker); the invariant checker flags it.
const poison = 0xDEADBEEFDEADBEEF

func buggyWorkload(bugAt uint64) Workload {
	return func(e *Engine, emit Emit) {
		var state uint64
		var step uint64
		var tick EventFunc
		tick = func(e *Engine) {
			step++
			state = state*31 + uint64(e.Intn(97))
			if bugAt > 0 && step == bugAt {
				state = poison // injected fault
			}
			emit("tick", state)
			e.Schedule(uint64(e.Intn(3)+1), 0, "tick", tick)
		}
		e.Schedule(0, 0, "tick", tick)
	}
}

func poisonInvariant(_ uint64, _ string, state uint64) error {
	if state == poison {
		return errors.New("state corrupted: poison marker observed")
	}
	return nil
}

func TestBugReproduction(t *testing.T) {
	const bugAt = 5000
	const maxSteps = 100000

	// Clean run passes.
	clean := RunWorkloadNoTrace(42, maxSteps, buggyWorkload(0), poisonInvariant)
	if clean.Err != nil || clean.Steps != maxSteps {
		t.Fatalf("clean run: err=%v steps=%d", clean.Err, clean.Steps)
	}

	// Faulty run is caught by the invariant.
	bad := RunWorkloadNoTrace(42, maxSteps, buggyWorkload(bugAt), poisonInvariant)
	if bad.Err == nil || bad.FailStep != bugAt {
		t.Fatalf("fault not caught: err=%v failStep=%d", bad.Err, bad.FailStep)
	}

	// Reproduction: identical seed and workload must fail at the exact
	// same step with the exact same trace hash.
	ok, repro := Reproduce(bad, maxSteps, buggyWorkload(bugAt), poisonInvariant)
	if !ok {
		t.Fatalf("not reproducible: %+v vs %+v", bad, repro)
	}
	if repro.Hash != bad.Hash || repro.FailStep != bugAt {
		t.Fatalf("repro diverged: %+v", repro)
	}
}

func TestTraceRecording(t *testing.T) {
	res := RunWorkload(11, 100, chainWorkload, nil)
	if len(res.Entries) != 100 {
		t.Fatalf("entries = %d", len(res.Entries))
	}
	for i, en := range res.Entries {
		if en.Step != uint64(i+1) || en.Tag != "tick" {
			t.Fatalf("entry %d: %+v", i, en)
		}
		if i > 0 && en.Time < res.Entries[i-1].Time {
			t.Fatal("time went backwards")
		}
	}
	if ReproReport(res, true) == "" {
		t.Fatal("empty report")
	}
}
