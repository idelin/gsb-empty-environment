package fiber

import (
	"fmt"
	"testing"
)

// runThreeFibers spawns 3 fibers that alternately Yield `rounds` times and
// returns the resulting scheduling log.
func runThreeFibers(rounds int) []Event {
	s := &Scheduler{}
	work := func(f *Fiber) {
		for i := 0; i < rounds; i++ {
			f.Yield()
		}
	}
	s.Spawn("A", work)
	s.Spawn("B", work)
	s.Spawn("C", work)
	s.Run()
	return s.Log()
}

// TestAlternatingYield verifies the acceptance scenario: 3 fibers
// alternately yield, and the scheduler interleaves them in strict
// round-robin order A, B, C, A, B, C, ...
func TestAlternatingYield(t *testing.T) {
	const rounds = 4
	log := runThreeFibers(rounds)

	// Expect: 3 SPAWN, then rounds * (3 RESUME + 3 YIELD), then
	// 3 RESUME + 3 EXIT for the final slices in which fibers return.
	want := []Event{}
	for i := 0; i < 3; i++ {
		want = append(want, Event{Tick: 0, Kind: EventSpawn, Fiber: i})
	}
	var tick uint64
	for r := 0; r < rounds; r++ {
		for id := 0; id < 3; id++ {
			tick++
			want = append(want,
				Event{Tick: tick, Kind: EventResume, Fiber: id},
				Event{Tick: tick, Kind: EventYield, Fiber: id},
			)
		}
	}
	for id := 0; id < 3; id++ {
		tick++
		want = append(want,
			Event{Tick: tick, Kind: EventResume, Fiber: id},
			Event{Tick: tick, Kind: EventExit, Fiber: id},
		)
	}

	if len(log) != len(want) {
		t.Fatalf("log length = %d, want %d", len(log), len(want))
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("event %d = %v, want %v", i, log[i], want[i])
		}
	}
}

// TestDeterministicReproducible runs the same workload many times and
// requires the tick-by-tick event sequence to be identical every time,
// eliminating concurrency nondeterminism.
func TestDeterministicReproducible(t *testing.T) {
	const runs = 200
	base := runThreeFibers(10)
	for i := 0; i < runs; i++ {
		got := runThreeFibers(10)
		if len(got) != len(base) {
			t.Fatalf("run %d: log length = %d, want %d", i, len(got), len(base))
		}
		for j := range base {
			if got[j] != base[j] {
				t.Fatalf("run %d diverges at event %d: got %v, want %v", i, j, got[j], base[j])
			}
		}
	}
}

// TestVirtualClock verifies the virtual clock advances by exactly one
// time slice per dispatch and that ticks are strictly increasing.
func TestVirtualClock(t *testing.T) {
	s := &Scheduler{SliceMicros: 5}
	s.Spawn("only", func(f *Fiber) {
		f.Yield()
		f.Yield()
	})
	s.Run()

	if s.Clock() != 15 { // 3 dispatches * 5us
		t.Fatalf("clock = %dus, want 15us", s.Clock())
	}
	var prev, prevResume uint64
	for i, e := range s.Log() {
		if e.Kind == EventSpawn {
			continue
		}
		if e.Tick < prev {
			t.Fatalf("event %d: tick %d goes backwards from %d", i, e.Tick, prev)
		}
		if e.Kind == EventResume {
			if e.Tick <= prevResume {
				t.Fatalf("event %d: resume tick %d not greater than previous resume %d", i, e.Tick, prevResume)
			}
			prevResume = e.Tick
		}
		prev = e.Tick
	}
}

// TestFiberStates verifies lifecycle state transitions.
func TestFiberStates(t *testing.T) {
	s := &Scheduler{}
	var observed []State
	s.Spawn("probe", func(f *Fiber) {
		observed = append(observed, f.State()) // Running
		f.Yield()
		observed = append(observed, f.State()) // Running again after resume
	})
	f := s.fibers[0]
	if f.State() != Ready {
		t.Fatalf("before Run: state = %v, want Ready", f.State())
	}
	s.Run()
	if f.State() != Dead {
		t.Fatalf("after Run: state = %v, want Dead", f.State())
	}
	if len(observed) != 2 || observed[0] != Running || observed[1] != Running {
		t.Fatalf("observed states = %v, want [Running Running]", observed)
	}
}

// TestExitWithoutYield covers fibers that return without ever yielding.
func TestExitWithoutYield(t *testing.T) {
	s := &Scheduler{}
	for i := 0; i < 3; i++ {
		s.Spawn(fmt.Sprintf("f%d", i), func(f *Fiber) {})
	}
	s.Run()
	exits := 0
	for _, e := range s.Log() {
		if e.Kind == EventExit {
			exits++
		}
	}
	if exits != 3 {
		t.Fatalf("exit events = %d, want 3", exits)
	}
}

// TestUnevenLifetimes verifies round-robin skips dead fibers while
// remaining deterministic.
func TestUnevenLifetimes(t *testing.T) {
	s := &Scheduler{}
	s.Spawn("short", func(f *Fiber) { f.Yield() })
	s.Spawn("long", func(f *Fiber) {
		for i := 0; i < 5; i++ {
			f.Yield()
		}
	})
	s.Run()

	// After "short" dies, every slice must go to "long" (id 1).
	log := s.Log()
	dead := false
	for _, e := range log {
		if e.Kind == EventExit && e.Fiber == 0 {
			dead = true
		}
		if dead && e.Kind == EventResume && e.Fiber != 1 {
			t.Fatalf("fiber 0 resumed after exit: %v", e)
		}
	}
}
