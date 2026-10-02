package main

import (
	"math"
	"strings"
	"testing"
)

func runDefaultScenario(t *testing.T) Report {
	t.Helper()
	numNodes, suspicionTimeout, injections, cycles := DefaultScenario()
	cluster := NewCluster(numNodes, suspicionTimeout)
	observations := cluster.Run(injections, cycles)
	return BuildReport(Analyze(injections, observations))
}

// 验收：10 次宕机的平均检测时间（MTTD）为 2.4 个协议周期。
func TestMTTDIsTwoPointFourCycles(t *testing.T) {
	report := runDefaultScenario(t)
	if math.Abs(report.MTTD-2.4) > 1e-9 {
		t.Errorf("MTTD = %.4f cycles, want 2.4", report.MTTD)
	}
	if report.Detected != 10 || report.Missed != 0 {
		t.Errorf("detected=%d missed=%d, want 10/0", report.Detected, report.Missed)
	}
}

// 验收：5 次短暂丢包扰动下误报率为 0%。
func TestFalsePositiveRateIsZero(t *testing.T) {
	report := runDefaultScenario(t)
	if report.DisturbanceCount != 5 {
		t.Fatalf("disturbances = %d, want 5", report.DisturbanceCount)
	}
	if report.FalsePositives != 0 || report.FalsePositiveRate != 0 {
		t.Errorf("false positives = %d (rate %.2f%%), want 0 (0%%)",
			report.FalsePositives, report.FalsePositiveRate*100)
	}
}

// 检测时延分布：探测前注入 2 周期检出，探测后注入 3 周期检出。
func TestDetectionLatencyDistribution(t *testing.T) {
	report := runDefaultScenario(t)
	twos, threes := 0, 0
	for _, rec := range report.Records {
		if rec.Injection.Type != CrashInjection {
			continue
		}
		want := 2.0
		if rec.Injection.Offset > 0.5 {
			want = 3.0
		}
		if rec.DetectionLatency != want {
			t.Errorf("event %d (offset %.1f): latency %.1f, want %.1f",
				rec.Injection.ID, rec.Injection.Offset, rec.DetectionLatency, want)
		}
		switch rec.DetectionLatency {
		case 2:
			twos++
		case 3:
			threes++
		}
	}
	if twos != 6 || threes != 4 {
		t.Errorf("latency distribution: %dx2 + %dx3, want 6x2 + 4x3", twos, threes)
	}
	if report.MinLatency != 2 || report.MaxLatency != 3 {
		t.Errorf("min/max latency = %.1f/%.1f, want 2/3", report.MinLatency, report.MaxLatency)
	}
}

// 扰动鲁棒性：短暂丢包被间接探测吸收；整周期丢包仅触发瞬时怀疑并被澄清。
func TestDisturbancesAbsorbedOrRefuted(t *testing.T) {
	report := runDefaultScenario(t)
	absorbed, refuted := 0, 0
	for _, rec := range report.Records {
		if rec.Injection.Type != PacketLossInjection {
			continue
		}
		if rec.MarkedCycle >= 0 {
			t.Errorf("disturbance event %d: healthy node marked failed at cycle %d",
				rec.Injection.ID, rec.MarkedCycle)
		}
		if rec.FirstSuspectCycle >= 0 {
			if rec.RefutedCycle < 0 {
				t.Errorf("disturbance event %d: suspected but never refuted", rec.Injection.ID)
			}
			refuted++
		} else {
			absorbed++
		}
	}
	if absorbed != 3 || refuted != 2 || report.TransientSuspicion != 2 {
		t.Errorf("absorbed=%d refuted=%d (report transient=%d), want 3/2/2",
			absorbed, refuted, report.TransientSuspicion)
	}
}

// 时序一致性：注入事件的首次怀疑不早于注入，最终标记不早于首次怀疑。
func TestEventOrdering(t *testing.T) {
	report := runDefaultScenario(t)
	for _, rec := range report.Records {
		if rec.FirstSuspectCycle >= 0 && rec.FirstSuspectCycle < rec.Injection.Cycle {
			t.Errorf("event %d: suspected at %d before injection at %d",
				rec.Injection.ID, rec.FirstSuspectCycle, rec.Injection.Cycle)
		}
		if rec.MarkedCycle >= 0 && rec.MarkedCycle < rec.FirstSuspectCycle {
			t.Errorf("event %d: marked at %d before first suspicion at %d",
				rec.Injection.ID, rec.MarkedCycle, rec.FirstSuspectCycle)
		}
	}
}

// 报告内容：包含关键指标与逐事件记录。
func TestReportContents(t *testing.T) {
	report := runDefaultScenario(t)
	out := report.String()
	for _, want := range []string{
		"MTTD (mean time to detect): 2.40 cycles",
		"rate 0.00%",
		"10/10 crashes detected, 0 missed",
		"crash",
		"packet-loss",
		"FALSE-POSITIVE", // 表头之外不应出现？此处校验不存在于结果行
	} {
		if want == "FALSE-POSITIVE" {
			if strings.Contains(out, want) {
				t.Errorf("report unexpectedly contains %q", want)
			}
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}
}

// 分析仪单元测试：漏检与误报能被正确识别。
func TestAnalyzerDetectsMissAndFalsePositive(t *testing.T) {
	injections := []Injection{
		{ID: 0, Type: CrashInjection, Target: 0, Cycle: 5},
		{ID: 1, Type: PacketLossInjection, Target: 1, Cycle: 8, Duration: 0.4},
	}
	observations := []Observation{
		{Kind: MarkedFailed, Target: 1, Cycle: 10}, // 存活节点被标记 -> 误报
	}
	records := Analyze(injections, observations)
	if !records[0].Missed {
		t.Error("crash event should be reported as missed")
	}
	if !records[1].FalsePositive {
		t.Error("disturbance event should be reported as false positive")
	}
	report := BuildReport(records)
	if report.FalsePositiveRate != 1.0 {
		t.Errorf("false positive rate = %.2f, want 1.0", report.FalsePositiveRate)
	}
	if report.Missed != 1 || report.Detected != 0 {
		t.Errorf("missed=%d detected=%d, want 1/0", report.Missed, report.Detected)
	}
}
