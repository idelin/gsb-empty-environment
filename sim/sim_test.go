package sim

import (
	"testing"
)

// TestDeterministicMillionSteps runs a 1,000,000-step simulation twice
// with the same seed and requires bit-identical outcomes.
func TestDeterministicMillionSteps(t *testing.T) {
	steps := 1_000_000
	if testing.Short() {
		steps = 100_000
	}
	cfg := Config{
		Seed:       42,
		Steps:      steps,
		NumBitmaps: 4,
		KeySpace:   1 << 16,
	}
	stats1, fail1 := New(cfg).Run()
	if fail1 != nil {
		t.Fatalf("unexpected failure: %v", fail1)
	}
	stats2, fail2 := Reproduce(cfg)
	if fail2 != nil {
		t.Fatalf("unexpected failure on reproduce: %v", fail2)
	}
	if stats1.Hash != stats2.Hash {
		t.Fatalf("non-deterministic run: hash %x != %x", stats1.Hash, stats2.Hash)
	}
	if stats1.Events != stats2.Events || stats1.Checkpoints != stats2.Checkpoints {
		t.Fatalf("non-deterministic stats: %+v vs %+v", stats1, stats2)
	}
	if stats1.Events < uint64(steps) {
		t.Fatalf("expected at least %d events, got %d", steps, stats1.Events)
	}
	t.Logf("million-step run: events=%d adds=%d removes=%d queries=%d checkpoints=%d hash=%x",
		stats1.Events, stats1.Adds, stats1.Removes, stats1.Queries, stats1.Checkpoints, stats1.Hash)
}

// TestDistinctSeedsDiverge ensures the hash actually depends on the seed.
func TestDistinctSeedsDiverge(t *testing.T) {
	base := Config{Steps: 20_000, KeySpace: 1 << 16}
	s1, f1 := New(Config{Seed: 1, Steps: base.Steps, KeySpace: base.KeySpace}).Run()
	s2, f2 := New(Config{Seed: 2, Steps: base.Steps, KeySpace: base.KeySpace}).Run()
	if f1 != nil || f2 != nil {
		t.Fatalf("unexpected failures: %v / %v", f1, f2)
	}
	if s1.Hash == s2.Hash {
		t.Fatal("different seeds produced identical hashes")
	}
}

// TestGoldenRegressionHash pins the exact hash of a fixed configuration.
// Any behavioral change in the bitmap or the simulator breaks this test.
func TestGoldenRegressionHash(t *testing.T) {
	cfg := Config{Seed: 7, Steps: 50_000, NumBitmaps: 4, KeySpace: 1 << 16}
	stats, fail := New(cfg).Run()
	if fail != nil {
		t.Fatalf("unexpected failure: %v", fail)
	}
	const wantHash = uint64(0xa7af39e2e2e1c784)
	if stats.Hash != wantHash {
		t.Fatalf("regression: hash = %x, want %x", stats.Hash, wantHash)
	}
}

// TestFaultInjectionReproduction injects a deterministic fault, checks
// that it is detected, and verifies that re-running the same config
// reproduces the exact same failure.
func TestFaultInjectionReproduction(t *testing.T) {
	cfg := Config{
		Seed:        1234,
		Steps:       50_000,
		NumBitmaps:  4,
		KeySpace:    1 << 16,
		InjectFault: true,
		FaultStep:   25_000,
	}
	_, fail := New(cfg).Run()
	if fail == nil {
		t.Fatal("fault was not detected")
	}
	if fail.Step > uint64(cfg.FaultStep)+2 {
		t.Fatalf("fault detected too late: step %d, injected at %d", fail.Step, cfg.FaultStep)
	}
	// Reproduction must be exact.
	_, fail2 := Reproduce(cfg)
	if fail2 == nil {
		t.Fatal("fault not reproduced")
	}
	if *fail != *fail2 {
		t.Fatalf("irreproducible failure: %+v vs %+v", fail, fail2)
	}
	// Same seed without injection must be clean, proving the injected
	// fault (not the workload) caused the failure.
	clean := cfg
	clean.InjectFault = false
	if _, fail3 := New(clean).Run(); fail3 != nil {
		t.Fatalf("clean run failed: %v", fail3)
	}
	t.Logf("fault reproduced deterministically: %v", fail)
}

// TestLargeKeySpace exercises the sparse (map-backed) reference model
// over the full 32-bit key space.
func TestLargeKeySpace(t *testing.T) {
	cfg := Config{
		Seed:        99,
		Steps:       20_000,
		NumBitmaps:  3,
		KeySpace:    1<<32 - 1,
		MaxDenseRun: 64,
	}
	stats, fail := New(cfg).Run()
	if fail != nil {
		t.Fatalf("unexpected failure: %v", fail)
	}
	stats2, _ := Reproduce(cfg)
	if stats.Hash != stats2.Hash {
		t.Fatal("large key space run not deterministic")
	}
}

// TestTinyKeySpace stresses boundary behavior when the key space is
// smaller than a single container.
func TestTinyKeySpace(t *testing.T) {
	cfg := Config{
		Seed:        5,
		Steps:       50_000,
		NumBitmaps:  2,
		KeySpace:    64,
		MaxDenseRun: 64,
	}
	if _, fail := New(cfg).Run(); fail != nil {
		t.Fatalf("unexpected failure: %v", fail)
	}
}

// TestNoLeakAfterDrain verifies that after a simulation all event state
// is drained and the bitmaps release every container when emptied.
func TestNoLeakAfterDrain(t *testing.T) {
	cfg := Config{Seed: 17, Steps: 50_000, NumBitmaps: 4, KeySpace: 1 << 16}
	s := New(cfg)
	if _, fail := s.Run(); fail != nil {
		t.Fatalf("unexpected failure: %v", fail)
	}
	if s.queue.Len() != 0 {
		t.Fatalf("event queue not drained: %d events left", s.queue.Len())
	}
	for i, bm := range s.bitmaps {
		bm.RemoveRange(0, 1<<32)
		if !bm.IsEmpty() {
			t.Fatalf("bitmap %d not empty after full remove", i)
		}
		if got := bm.Stats(); got.Containers != 0 || got.SizeInBytes != 0 {
			t.Fatalf("bitmap %d leaked containers: %+v", i, got)
		}
		if s.models[i].count() != 0 {
			// The model is independent; clear it the same way.
			s.models[i].removeRange(0, cfg.KeySpace-1)
			if s.models[i].count() != 0 {
				t.Fatalf("model %d leaked entries", i)
			}
		}
	}
}

// TestEventOrdering checks that events are applied in (Time, Seq) order.
func TestEventOrdering(t *testing.T) {
	s := New(Config{Seed: 3, Steps: 10_000, KeySpace: 1 << 12})
	var lastTime, lastSeq uint64
	first := true
	for step := 0; step < 10_000; step++ {
		s.generate()
		if s.queue.Len() == 0 {
			continue
		}
		e := s.queue[0]
		if !first {
			if e.Time < lastTime || (e.Time == lastTime && e.Seq < lastSeq) {
				t.Fatalf("events out of order: (%d,%d) after (%d,%d)", e.Time, e.Seq, lastTime, lastSeq)
			}
		}
		first = false
		lastTime, lastSeq = e.Time, e.Seq
		if f := s.step(); f != nil {
			t.Fatalf("unexpected failure: %v", f)
		}
	}
}
