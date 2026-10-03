package sim

import (
	"runtime"
	"runtime/debug"
	"testing"

	"aries/recovery"
	"aries/wal"
)

func crashConfig(numTxns int) WorkloadConfig {
	return WorkloadConfig{
		NumTxns:         numTxns,
		NumPages:        32,
		MaxOpsPerTxn:    6,
		CommitPct:       60,
		AbortPct:        25,
		CrashAt:         4000,
		CheckpointEvery: 500,
		FlushEvery:      130,
		PostCrashTxns:   20,
	}
}

// runToCompletion runs a workload and asserts the simulation finished
// cleanly with no leaked work.
func runToCompletion(t *testing.T, s *Sim) {
	t.Helper()
	s.Run(1 << 62)
	if s.Fail != nil {
		t.Fatalf("simulation failed: %v", s.Fail)
	}
	if s.PendingEvents() != 0 {
		t.Fatalf("%d events leaked in the queue", s.PendingEvents())
	}
}

// assertRecoveredState verifies the durable state against the
// independent committed-state model and checks PageLSN consistency.
func assertRecoveredState(t *testing.T, s *Sim) {
	t.Helper()
	// Reuse the records decoded during recovery; append any post-crash
	// records so the model sees the full committed history.
	recs := append([]*wal.Record(nil), s.LastResult.Records...)
	all, err := wal.New(s.Store, 1).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) > len(recs) {
		recs = append(recs, all[len(recs):]...)
	}
	model := recovery.ExpectedCommittedState(recs)
	maxLSN := make(map[uint64]uint64)
	for _, r := range recs {
		if (r.Type == wal.RecUpdate || r.Type == wal.RecCLR) && r.LSN > maxLSN[r.PageID] {
			maxLSN[r.PageID] = r.LSN
		}
	}
	snap := s.Disk.Snapshot()
	for page, want := range model {
		if got := snap[page].Value; got != want {
			t.Errorf("page %d = %d, committed model wants %d", page, got, want)
		}
	}
	for page, img := range snap {
		if _, ok := model[page]; !ok && img.Value != 0 {
			t.Errorf("page %d = %d on disk but absent from committed model", page, img.Value)
		}
		if img.PageLSN != maxLSN[page] {
			t.Errorf("page %d PageLSN = %d, want %d (max log LSN touching it)", page, img.PageLSN, maxLSN[page])
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	cfg := crashConfig(300)
	var hashes []uint64
	var traces [][]string
	for run := 0; run < 2; run++ {
		s := New(42)
		s.Traced = true
		s.LoadWorkload(cfg)
		runToCompletion(t, s)
		if s.Recoveries != 1 {
			t.Fatalf("run %d: %d recoveries, want 1", run, s.Recoveries)
		}
		hashes = append(hashes, s.StateHash())
		traces = append(traces, s.Trace())
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("non-deterministic: hashes %x vs %x", hashes[0], hashes[1])
	}
	if len(traces[0]) != len(traces[1]) {
		t.Fatalf("trace lengths differ: %d vs %d", len(traces[0]), len(traces[1]))
	}
	for i := range traces[0] {
		if traces[0][i] != traces[1][i] {
			t.Fatalf("trace diverges at event %d: %q vs %q", i, traces[0][i], traces[1][i])
		}
	}
}

func TestCrashRecoveryCorrectness(t *testing.T) {
	s := New(7)
	s.LoadWorkload(crashConfig(400))
	runToCompletion(t, s)
	if s.Recoveries != 1 {
		t.Fatalf("%d recoveries, want 1", s.Recoveries)
	}
	s.Pool.FlushAll()
	assertRecoveredState(t, s)
	if len(s.LastResult.TxnTable) != 0 {
		t.Errorf("txn table not empty after recovery")
	}
	if dirty := s.Pool.DirtyPages(); len(dirty) != 0 {
		t.Errorf("dirty pages leaked: %v", dirty)
	}
	if s.DB.ActiveTxns() != 0 {
		t.Errorf("%d active txns leaked", s.DB.ActiveTxns())
	}
}

func TestScenarioReproduction(t *testing.T) {
	sc := Scenario{Seed: 1234, Config: crashConfig(200)}
	exp, err := RunScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	sc.Expected = exp

	// Serialize as a bug report would, then reproduce.
	data, err := MarshalScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := UnmarshalScenario(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := Reproduce(loaded); err != nil {
		t.Fatalf("reproduction failed: %v", err)
	}

	// A tampered expectation must be detected.
	loaded.Expected.StateHash ^= 0xFF
	if err := Reproduce(loaded); err == nil {
		t.Fatal("tampered expectation not detected")
	}
}

// TestMillionStepRegression drives over a million discrete events
// through the full stack (txns, WAL, checkpoints, flushes, crash,
// recovery) and asserts determinism and correctness. With -short it
// runs a reduced workload.
func TestMillionStepRegression(t *testing.T) {
	// The workload allocates millions of log frames; relax GC to keep
	// the regression fast without affecting production defaults.
	oldGC := debug.SetGCPercent(800)
	defer debug.SetGCPercent(oldGC)
	cfg := WorkloadConfig{
		NumTxns:         150000,
		NumPages:        4096,
		MaxOpsPerTxn:    10,
		CommitPct:       60,
		AbortPct:        25,
		CrashAt:         900000,
		CheckpointEvery: 50000,
		FlushEvery:      33333,
		PostCrashTxns:   100,
	}
	if testing.Short() {
		cfg.NumTxns = 4000
		cfg.CrashAt = 30000
		cfg.CheckpointEvery = 5000
		cfg.FlushEvery = 3000
		cfg.PostCrashTxns = 10
	}
	var hashes []uint64
	var steps uint64
	for run := 0; run < 2; run++ {
		s := New(2026)
		s.LoadWorkload(cfg)
		runToCompletion(t, s)
		if run == 0 {
			steps = s.Steps()
		} else if s.Steps() != steps {
			t.Fatalf("step counts differ: %d vs %d", steps, s.Steps())
		}
		if s.Recoveries != 1 {
			t.Fatalf("run %d: %d recoveries, want 1", run, s.Recoveries)
		}
		s.Pool.FlushAll()
		hashes = append(hashes, s.StateHash())
		if run == 0 {
			assertRecoveredState(t, s)
		}
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("non-deterministic: %x vs %x", hashes[0], hashes[1])
	}
	if !testing.Short() && steps < 1000000 {
		t.Fatalf("only %d steps, want >= 1,000,000", steps)
	}
	t.Logf("regression: %d steps, state hash %x", steps, hashes[0])
}

// TestNoGoroutineLeaks asserts the whole stack is goroutine-clean.
func TestNoGoroutineLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	s := New(99)
	s.LoadWorkload(crashConfig(100))
	runToCompletion(t, s)
	s.Pool.FlushAll()
	after := runtime.NumGoroutine()
	if after != before {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, after)
	}
}
