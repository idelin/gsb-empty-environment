package sim

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// chainWorkload schedules one "tick" event; every tick schedules the
// next one 1 tick later and folds an RNG draw into the hash, forming a
// deterministic n-step chain.
func chainWorkload(e *Engine) {
	e.Handle("tick", func(e *Engine, ev Event) {
		e.Observe(e.Uint64n(1 << 32))
		e.Schedule(1, "tick", nil)
	})
	e.Schedule(0, "tick", nil)
}

func TestEventOrdering(t *testing.T) {
	e := New(1)
	var got []uint64
	e.Handle("e", func(e *Engine, ev Event) {
		got = append(got, ev.Time*1000+ev.Seq)
	})
	// Schedule out of order: later time first, equal times keep
	// schedule order via Seq.
	e.Schedule(5, "e", nil)
	e.Schedule(1, "e", nil)
	e.Schedule(3, "e", nil)
	e.Schedule(1, "e", nil)
	e.Drain()
	// Expected (time, seq): (1,1) (1,3) (3,2) (5,0)
	want := []uint64{1001, 1003, 3002, 5000}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDeterministicDoubleRun(t *testing.T) {
	a := Replay(42, nil, chainWorkload, 100_000)
	b := Replay(42, nil, chainWorkload, 100_000)
	if d := Diff(a, b); d != "" {
		t.Fatalf("identical configs diverged: %s", d)
	}
	if len(a.Steps) != 100_000 {
		t.Fatalf("steps: %d", len(a.Steps))
	}
	// A different seed must diverge.
	c := Replay(43, nil, chainWorkload, 100_000)
	if Diff(a, c) == "" {
		t.Fatal("different seeds produced identical traces")
	}
}

func TestGoldenHash(t *testing.T) {
	// Regression anchor: the exact fingerprint of 1,000,000
	// deterministic steps. If any behavioral change alters event
	// ordering, RNG consumption or hashing, this test fails.
	rec := Replay(2024, nil, chainWorkload, 1_000_000)
	const golden = uint64(0xe07d02726f30c6ba)
	if rec.FinalHash() != golden {
		t.Fatalf("hash drift: got %016x, want %016x", rec.FinalHash(), golden)
	}
}

func TestFaultInjectionAndReproduction(t *testing.T) {
	faults := []FaultFunc{DropEvery("tick", 977)}
	baseline := Replay(7, nil, chainWorkload, 50_000)
	faulty := Replay(7, faults, chainWorkload, 50_000)

	d := Diff(baseline, faulty)
	if d == "" {
		t.Fatal("fault injection had no observable effect")
	}
	t.Logf("divergence: %s", d)

	// Bug reproduction: replaying the faulty configuration must
	// reproduce the faulty trace bit-for-bit.
	repro := Replay(7, faults, chainWorkload, 50_000)
	if d := Diff(faulty, repro); d != "" {
		t.Fatalf("faulty run not reproducible: %s", d)
	}
}

func TestDelayFault(t *testing.T) {
	faults := []FaultFunc{DelayKind("tick", 5)}
	a := Replay(9, nil, chainWorkload, 1_000)
	b := Replay(9, faults, chainWorkload, 1_000)
	if Diff(a, b) == "" {
		t.Fatal("delay fault had no effect")
	}
}

func TestConcurrentSchedule(t *testing.T) {
	e := New(11)
	var mu sync.Mutex
	count := 0
	e.Handle("work", func(e *Engine, ev Event) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	const producers = 8
	const perProducer = 500
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				e.Schedule(uint64(i%10), "work", nil)
				_ = e.Uint64n(100) // exercise the RNG concurrently
				_ = e.Now()
			}
		}(p)
	}
	wg.Wait()
	if got := e.Pending(); got != producers*perProducer {
		t.Fatalf("pending: got %d, want %d", got, producers*perProducer)
	}
	e.Drain()
	mu.Lock()
	defer mu.Unlock()
	if count != producers*perProducer {
		t.Fatalf("executed: got %d, want %d", count, producers*perProducer)
	}
	if e.Pending() != 0 {
		t.Fatal("queue not drained")
	}
}

func TestRunUntil(t *testing.T) {
	e := New(3)
	e.Handle("tick", func(e *Engine, ev Event) {
		e.Schedule(1, "tick", nil)
	})
	e.Schedule(0, "tick", nil)
	e.RunUntil(100)
	if got := e.Executed(); got != 101 { // ticks 0..100 inclusive
		t.Fatalf("executed %d, want 101", got)
	}
	if e.Now() != 100 {
		t.Fatalf("now: %d", e.Now())
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		Replay(int64(i), nil, chainWorkload, 1_000)
	}
	// Allow any transient runtime goroutines to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestMillionStepRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping million-step regression in -short mode")
	}
	rec := Replay(2024, nil, chainWorkload, 1_000_000)
	if len(rec.Steps) != 1_000_000 {
		t.Fatalf("steps: %d", len(rec.Steps))
	}
	// Determinism across process runs is anchored by TestGoldenHash;
	// here we additionally verify a same-process rerun.
	again := Replay(2024, nil, chainWorkload, 1_000_000)
	if d := Diff(rec, again); d != "" {
		t.Fatalf("million-step run not deterministic: %s", d)
	}
}
