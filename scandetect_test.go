package scandetect

import (
	"math"
	"sync"
	"testing"
)

func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func TestInvalidConfig(t *testing.T) {
	base := DefaultConfig()
	cases := []Config{
		func() Config { c := base; c.SuccessProbBenign = 1.2; return c }(),
		func() Config { c := base; c.SuccessProbScanner = 0.9; return c }(),
		func() Config { c := base; c.FalsePositiveRate = 0; return c }(),
		func() Config { c := base; c.FalseNegativeRate = 1; return c }(),
		func() Config { c := base; c.MaxSources = 0; return c }(),
		func() Config { c := base; c.MaxTargetsPerSource = -1; return c }(),
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestThresholdValues(t *testing.T) {
	d, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	upper, lower := d.Thresholds()
	wantUpper := math.Log(0.99 / 0.01)
	wantLower := math.Log(0.01 / 0.99)
	if !approx(upper, wantUpper) || !approx(lower, wantLower) {
		t.Fatalf("thresholds = (%v,%v), want (%v,%v)", upper, lower, wantUpper, wantLower)
	}
	if !approx(d.stepFailure, math.Log(0.8/0.2)) {
		t.Fatalf("failure step = %v", d.stepFailure)
	}
	if !approx(d.stepSuccess, math.Log(0.2/0.8)) {
		t.Fatalf("success step = %v", d.stepSuccess)
	}
}

func TestConsecutiveFailuresDetectScanner(t *testing.T) {
	d, _ := New(DefaultConfig())
	const src = "10.0.0.9"
	var v Verdict
	for i := 0; i < 10; i++ {
		v = d.Observe(src, target(i), false)
	}
	if v != Scanner {
		t.Fatalf("verdict = %s, want scanner", v)
	}
	stats, ok := d.Stats(src)
	if !ok || stats.Verdict != Scanner || stats.Failures != 4 || stats.Successes != 0 {
		t.Fatalf("unexpected stats: %+v ok=%v", stats, ok)
	}
}

func TestRepeatedTargetCountedOnce(t *testing.T) {
	d, _ := New(DefaultConfig())
	const src = "10.0.0.1"
	// 50 failing retries against one target must not accumulate evidence.
	for i := 0; i < 50; i++ {
		if v := d.Observe(src, "1.2.3.4:80", false); v != Pending {
			t.Fatalf("retry %d produced verdict %s", i, v)
		}
	}
	stats, _ := d.Stats(src)
	if stats.Attempts != 50 || stats.UniqueConnections != 1 || stats.Failures != 1 {
		t.Fatalf("dedup stats wrong: %+v", stats)
	}
	// A second distinct target still only contributes one more sample.
	d.Observe(src, "1.2.3.4:80", false)
	d.Observe(src, "1.2.3.5:80", false)
	stats, _ = d.Stats(src)
	if stats.UniqueConnections != 2 || stats.Failures != 2 {
		t.Fatalf("after new target: %+v", stats)
	}
}

func TestVerdictFreezesAfterDecision(t *testing.T) {
	d, _ := New(DefaultConfig())
	const src = "s"
	var v Verdict
	for i := 0; i < 5; i++ {
		v = d.Observe(src, target(i), false)
	}
	if v != Scanner {
		t.Fatalf("want scanner, got %s", v)
	}
	// Later successful connections cannot move a frozen scanner verdict.
	for i := 100; i < 110; i++ {
		if v := d.Observe(src, target(i), true); v != Scanner {
			t.Fatalf("frozen verdict changed to %s", v)
		}
	}
	stats, _ := d.Stats(src)
	if stats.Successes != 0 || stats.Failures != 4 {
		t.Fatalf("frozen state mutated: %+v", stats)
	}
}

func TestBenignVerdictOnSuccesses(t *testing.T) {
	d, _ := New(DefaultConfig())
	const src = "b"
	var v Verdict
	for i := 0; i < 10; i++ {
		v = d.Observe(src, target(i), true)
	}
	if v != Benign {
		t.Fatalf("want benign, got %s", v)
	}
}

func TestMaxSourcesEvictionResetsState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSources = 2
	d, _ := New(cfg)

	d.Observe("a", "t1", false)
	d.Observe("b", "t1", false)
	// Touch "a" so "b" is the LRU; adding "c" must evict "b".
	d.Observe("a", "t2", false)
	d.Observe("c", "t1", false)
	if d.Len() != 2 || d.Evictions() != 1 {
		t.Fatalf("len=%d evictions=%d", d.Len(), d.Evictions())
	}
	if _, ok := d.Stats("b"); ok {
		t.Fatal("evicted source b still tracked")
	}
	// "b" reappears and starts from a clean slate.
	if v := d.Observe("b", "t1", false); v != Pending {
		t.Fatalf("re-added source verdict = %s", v)
	}
	stats, ok := d.Stats("b")
	if !ok || stats.Attempts != 1 || stats.Failures != 1 || stats.Verdict != Pending {
		t.Fatalf("reset state wrong: %+v ok=%v", stats, ok)
	}
}

func TestMaxTargetsPerSourceBound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTargetsPerSource = 2
	d, _ := New(cfg)
	const src = "s"
	d.Observe(src, "t0", false)
	d.Observe(src, "t1", false)
	d.Observe(src, "t2", false)
	stats, _ := d.Stats(src)
	if stats.UniqueConnections != 2 {
		t.Fatalf("target set size = %d, want 2", stats.UniqueConnections)
	}
	// t0 was forgotten, so it is treated as a brand-new target again.
	before, _ := d.Stats(src)
	d.Observe(src, "t0", false)
	after, _ := d.Stats(src)
	if after.Failures != before.Failures+1 {
		t.Fatalf("forgotten target not re-counted: before=%d after=%d", before.Failures, after.Failures)
	}
}

func TestConcurrentObserve(t *testing.T) {
	d, _ := New(DefaultConfig())
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			src := "src-" + string(rune('a'+g))
			for i := 0; i < 200; i++ {
				d.Observe(src, target(i), i%3 == 0)
			}
		}(g)
	}
	wg.Wait()
	if d.Len() != 16 {
		t.Fatalf("len = %d, want 16", d.Len())
	}
}

func target(i int) string {
	return "host-" + string(rune('a'+i%26)) + ":" + string(rune('0'+i/26))
}
