package sim

import (
	"testing"

	"example.org/scandetect"
)

func defaultParams(trials int) Params {
	cfg := scandetect.DefaultConfig()
	return Params{
		Trials:            trials,
		Horizon:           500,
		ScanSuccessProb:   cfg.SuccessProbScanner,
		BenignSuccessProb: cfg.SuccessProbBenign,
		BenignServicePool: 25,
		MaxSources:        trials*2 + 16,
		Seed:              20261111,
		Config:            cfg,
	}
}

func TestRunValidation(t *testing.T) {
	const trials = 4000
	report, err := Run(defaultParams(trials))
	if err != nil {
		t.Fatal(err)
	}

	alpha := report.Params.Config.FalsePositiveRate
	fpr := report.Benign.Rate()
	// SPRT guarantees FPR <= alpha in the infinite-horizon sense; allow headroom
	// for Monte Carlo noise and the finite horizon (use 3x the nominal rate).
	if fpr > 3*alpha {
		t.Fatalf("benign false-positive rate %.4f exceeds bound %.4f", fpr, 3*alpha)
	}
	// Scanners must be caught almost always and within very few connections.
	if got := float64(report.Scanner.ScannerHits) / trials; got < 0.95 {
		t.Fatalf("scanner detection rate %.4f too low", got)
	}
	if report.Scanner.Undecided != 0 {
		t.Fatalf("%d scanners undecided within horizon", report.Scanner.Undecided)
	}
	if mean := report.Scanner.MeanDecisionRT(); mean > 8 {
		t.Fatalf("mean scanner decision latency %.2f too high", mean)
	}
	if p95 := report.Scanner.PercentileDecisionRT(0.95); p95 > 15 {
		t.Fatalf("p95 scanner decision latency %d too high", p95)
	}
	// Benign clients should quickly settle on the benign verdict.
	if got := float64(report.Benign.BenignHits) / trials; got < 0.95 {
		t.Fatalf("benign correct-verdict rate %.4f too low", got)
	}
}

func TestRepeatedConnectionsDoNotInflateLatency(t *testing.T) {
	// A tiny service pool means almost every benign event is a duplicate;
	// the detector must still reach the benign verdict from unique targets.
	p := defaultParams(800)
	p.BenignServicePool = 3
	p.Horizon = 2000
	report, err := Run(p)
	if err != nil {
		t.Fatal(err)
	}
	if fpr := report.Benign.Rate(); fpr > 3*p.Config.FalsePositiveRate {
		t.Fatalf("FPR with tiny pool %.4f too high", fpr)
	}
	for _, tr := range report.Benign.DecisionRTS {
		if tr > 3 {
			t.Fatalf("decision required %d unique conns from a 3-service pool", tr)
		}
	}
}

func TestRunRejectsBadParams(t *testing.T) {
	p := defaultParams(10)
	p.MaxSources = 5
	if _, err := Run(p); err == nil {
		t.Fatal("expected error when MaxSources <= Trials")
	}
}

func TestEvictedSourceRecounts(t *testing.T) {
	// Exercise the memory cap directly: an evicted source restarts at zero.
	cfg := scandetect.DefaultConfig()
	cfg.MaxSources = 1
	det, err := scandetect.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	det.Observe("a", "t1", false)
	det.Observe("b", "t1", false) // evicts "a"
	det.Observe("a", "t1", false) // "a" re-created fresh
	stats, ok := det.Stats("a")
	if !ok {
		t.Fatal("source a missing")
	}
	if stats.Attempts != 1 || stats.Failures != 1 || stats.Verdict != scandetect.Pending {
		t.Fatalf("evicted source did not restart: %+v", stats)
	}
	if det.Evictions() != 2 {
		t.Fatalf("evictions = %d, want 2", det.Evictions())
	}
}
