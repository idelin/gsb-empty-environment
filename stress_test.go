package cachebench

import (
	"strings"
	"testing"
)

// TestStressHighConcurrency 验收测试：高并发读写压力下，
// 度量模块须完整记录核心指标，报告须定位命中率拐点与驱逐突变区间。
func TestStressHighConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in -short mode")
	}
	report := RunStressScenario(DefaultStressConfig())

	data, err := report.JSON()
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	t.Logf("stress report:\n%s", data)

	tot := report.Totals
	if tot.Gets == 0 || tot.Hits == 0 || tot.Misses == 0 {
		t.Fatalf("incomplete counters: %+v", tot)
	}
	if tot.Evictions == 0 {
		t.Fatal("expected evictions under cold-key flood")
	}
	if report.GetLatency.Count != tot.Gets {
		t.Fatalf("get latency samples = %d, want %d", report.GetLatency.Count, tot.Gets)
	}
	if report.SetLatency.Count != tot.Sets {
		t.Fatalf("set latency samples = %d, want %d", report.SetLatency.Count, tot.Sets)
	}
	if report.GetLatency.P99Us > report.GetLatency.MaxUs {
		t.Fatalf("p99 (%dus) exceeds max (%dus)",
			report.GetLatency.P99Us, report.GetLatency.MaxUs)
	}

	infl := report.Analysis.HitRateInflection
	if infl == nil {
		t.Fatal("hit-rate inflection point not detected")
	}
	if infl.Drop < 0.3 {
		t.Fatalf("inflection drop = %v, want >= 0.3", infl.Drop)
	}
	t.Logf("hit-rate inflection at %.0f-%.0fms: %.2f -> %.2f (drop %.2f)",
		infl.StartMs, infl.EndMs, infl.HitRateBefore, infl.HitRateAfter, infl.Drop)

	spikes := report.Analysis.EvictionSpikes
	if len(spikes) == 0 {
		t.Fatal("no eviction spike interval detected")
	}
	for _, s := range spikes {
		t.Logf("eviction spike %.0f-%.0fms: %d evictions in %d windows",
			s.StartMs, s.EndMs, s.Evictions, s.Windows)
	}

	if report.Memory.CacheFragmentationRate <= 0 {
		t.Fatal("cache fragmentation rate not computed")
	}
	if report.Health.Score <= 0 || report.Health.Score > 100 {
		t.Fatalf("health score out of range: %v", report.Health.Score)
	}
	if !strings.Contains(string(data), "eviction_spike_intervals") {
		t.Fatal("report missing eviction_spike_intervals")
	}
}
