package lsmwatch

import (
	"strings"
	"testing"
)

func recordN(m *Monitor, n int, s GetSample) {
	for i := 0; i < n; i++ {
		m.RecordGet(s)
	}
}

func TestReadAmplificationSpikeAlert(t *testing.T) {
	m := NewMonitor(100)
	recordN(m, 100, GetSample{Reads: 1})
	m.Calibrate()

	recordN(m, 100, GetSample{Reads: 4})

	snap := m.Snapshot()
	if snap.WindowReadAmplification < 3.5 {
		t.Fatalf("窗口读放大应为 ~4.0，实际 %.2f", snap.WindowReadAmplification)
	}
	if len(snap.Alerts) != 1 {
		t.Fatalf("应触发 1 条告警，实际 %d 条", len(snap.Alerts))
	}
	a := snap.Alerts[0]
	if a.Kind != AlertReadAmplificationSpike {
		t.Fatalf("告警类型应为 %s，实际 %s", AlertReadAmplificationSpike, a.Kind)
	}
	if !strings.Contains(a.Suggestion, "Compaction") {
		t.Fatalf("告警应建议增加 Compaction 权重，实际: %s", a.Suggestion)
	}
}

func TestBloomFalsePositiveAlert(t *testing.T) {
	m := NewMonitor(100)
	recordN(m, 100, GetSample{Reads: 1, BloomChecks: 4, FalsePositives: 1})

	snap := m.Snapshot()
	if snap.WindowFalsePositiveRate < 0.10 {
		t.Fatalf("窗口误判率应为 0.25，实际 %.2f", snap.WindowFalsePositiveRate)
	}
	if len(snap.Alerts) != 1 || snap.Alerts[0].Kind != AlertBloomFilterRebuild {
		t.Fatalf("应触发 BLOOM_FILTER_REBUILD 告警，实际 %+v", snap.Alerts)
	}
	if !strings.Contains(snap.Alerts[0].Suggestion, "重建 Bloom Filter") {
		t.Fatalf("告警应建议重建 Filter，实际: %s", snap.Alerts[0].Suggestion)
	}
}

func TestNoAlertWhenHealthy(t *testing.T) {
	m := NewMonitor(100)
	recordN(m, 100, GetSample{Reads: 1, BloomChecks: 4})
	m.Calibrate()
	recordN(m, 100, GetSample{Reads: 1, BloomChecks: 4})

	if snap := m.Snapshot(); len(snap.Alerts) != 0 {
		t.Fatalf("健康状态下不应有告警，实际 %+v", snap.Alerts)
	}
}

func TestAlertRearmAfterRecovery(t *testing.T) {
	m := NewMonitor(100)
	recordN(m, 100, GetSample{Reads: 1})
	m.Calibrate()

	recordN(m, 100, GetSample{Reads: 4}) // 第一次突增
	recordN(m, 100, GetSample{Reads: 1}) // 恢复
	recordN(m, 100, GetSample{Reads: 4}) // 第二次突增

	if n := len(m.Snapshot().Alerts); n != 2 {
		t.Fatalf("恢复后再次突增应再次告警，共 2 条，实际 %d 条", n)
	}
}

func TestNoSpikeAlertWithoutBaseline(t *testing.T) {
	m := NewMonitor(100)
	recordN(m, 100, GetSample{Reads: 5}) // 未标定基线，不告警
	if n := len(m.Snapshot().Alerts); n != 0 {
		t.Fatalf("未标定基线不应告警，实际 %d 条", n)
	}
}
