package sim_test

import (
	"encoding/binary"
	"hash/fnv"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"aries/db"
	"aries/recovery"
	"aries/sim"
	"aries/storage"
	"aries/wal"
)

// TestMillionStepDeterminism runs a 1,000,000-event simulation twice
// with the same seed and requires bit-identical results.
func TestMillionStepDeterminism(t *testing.T) {
	run := func() (uint64, uint64) {
		s := sim.New(20261004)
		var counter uint64
		// Self-perpetuating event chains: every event schedules its
		// successor, so exactly maxSteps events run.
		var chain func(s *sim.Sim)
		chain = func(s *sim.Sim) {
			counter = counter*31 + uint64(s.RNG().Int63())
			s.Schedule(uint64(s.RNG().Intn(16)), chain)
		}
		for i := 0; i < 64; i++ {
			s.Schedule(uint64(s.RNG().Intn(8)), chain)
		}
		steps := s.Run(1_000_000)
		return steps, counter
	}
	steps1, c1 := run()
	steps2, c2 := run()
	if steps1 != 1_000_000 {
		t.Fatalf("executed %d steps, want 1000000", steps1)
	}
	if steps1 != steps2 || c1 != c2 {
		t.Fatalf("nondeterministic: (%d,%x) vs (%d,%x)", steps1, c1, steps2, c2)
	}
}

// TestEventOrdering checks events fire in (time, insertion) order.
func TestEventOrdering(t *testing.T) {
	s := sim.New(1)
	var order []int
	for i := 0; i < 100; i++ {
		i := i
		s.Schedule(uint64(100-i), func(s *sim.Sim) { order = append(order, i) })
	}
	s.Run(1000)
	for k, v := range order {
		if v != 99-k {
			t.Fatalf("events out of time order at %d: %v", k, order)
		}
	}
	// Ties: insertion order.
	s2 := sim.New(1)
	var ties []int
	for i := 0; i < 50; i++ {
		i := i
		s2.Schedule(5, func(s *sim.Sim) { ties = append(ties, i) })
	}
	s2.Run(1000)
	for i := 0; i < 50; i++ {
		if ties[i] != i {
			t.Fatal("same-time events not in insertion order")
		}
	}
}

// hashState hashes the WAL and disk image for equivalence checks.
func hashState(w *wal.WAL, disk *storage.Disk) uint64 {
	h := fnv.New64a()
	for _, r := range w.Records() {
		h.Write(r.Encode())
	}
	snap := disk.Snapshot()
	pages := make([]int, 0, len(snap))
	for p := range snap {
		pages = append(pages, int(p))
	}
	sort.Ints(pages)
	for _, p := range pages {
		var b [12]byte
		binary.BigEndian.PutUint32(b[0:], uint32(p))
		binary.BigEndian.PutUint64(b[4:], uint64(snap[uint32(p)].LSN))
		h.Write(b[:])
		h.Write(snap[uint32(p)].Data)
	}
	return h.Sum64()
}

// runCrashScenario executes a seeded workload ending in a crash,
// then recovers and returns the resulting state hash.
func runCrashScenario(t *testing.T, seed int64) (uint64, *sim.Workload, *recovery.Stats) {
	t.Helper()
	w, err := wal.Open(filepath.Join(t.TempDir(), "sim.wal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	disk := storage.NewDisk()
	database := db.New(w, disk)

	s := sim.New(seed)
	model := sim.RunWorkload(s, database, sim.WorkloadConfig{
		NumTxns:         80,
		MaxOpsPerTxn:    6,
		CheckpointEvery: 40,
		FlushProb:       35,
		AbortProb:       20,
		CrashAt:         100, // mid-timeline: txns are in flight
	})
	s.Run(1_000_000)
	if !model.Crashed {
		t.Fatal("workload did not crash")
	}

	st := recovery.NewEngine(w, disk).Recover()
	if err := model.VerifyAgainst(disk); err != nil {
		t.Fatalf("seed %d: %v (losers=%v)", seed, err, st.Losers)
	}
	return hashState(w, disk), model, st
}

// TestFaultReproduction: the same seed reproduces the exact same
// crash and recovery, bit for bit; recovery is always correct.
func TestFaultReproduction(t *testing.T) {
	const seed = 42
	h1, _, st1 := runCrashScenario(t, seed)
	h2, _, _ := runCrashScenario(t, seed)
	if h1 != h2 {
		t.Fatalf("same seed diverged: %x vs %x", h1, h2)
	}
	if len(st1.Losers) == 0 {
		t.Fatal("scenario produced no losers; tune the workload")
	}
	if st1.Redone == 0 {
		t.Fatal("scenario redid nothing; tune the workload")
	}
	// A spread of seeds must all recover correctly.
	for seed := int64(1); seed <= 25; seed++ {
		runCrashScenario(t, seed)
	}
}

// TestSimNoGoroutineLeak: simulations and recovery are synchronous
// and must not leak goroutines.
func TestSimNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	runCrashScenario(t, 7)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
