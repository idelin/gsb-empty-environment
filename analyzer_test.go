package swimlab

import (
	"math"
	"strings"
	"testing"
)

func runAcceptance(t *testing.T) Report {
	t.Helper()
	cfg, events, injections, periods := AcceptanceScenario()
	sim := NewSimulator(cfg)
	observations := sim.Run(periods, injections)

	a := NewAnalyzer()
	for _, e := range events {
		a.RegisterEvent(e)
	}
	for _, o := range observations {
		a.RecordObservation(o)
	}
	return a.Analyze()
}

func TestAcceptanceMTTD(t *testing.T) {
	rep := runAcceptance(t)
	if rep.DetectedCrashes != 10 {
		t.Fatalf("成功检测宕机数 = %d, 期望 10", rep.DetectedCrashes)
	}
	if rep.MissedCrashes != 0 {
		t.Fatalf("漏报数 = %d, 期望 0", rep.MissedCrashes)
	}
	if math.Abs(rep.AvgMTTD-2.4) > 1e-9 {
		t.Errorf("平均 MTTD = %.4f 周期, 期望 2.4 周期", rep.AvgMTTD)
	}
}

func TestAcceptanceFalsePositiveRate(t *testing.T) {
	rep := runAcceptance(t)
	if rep.FalsePositiveRate != 0 {
		t.Errorf("误报率 = %.2f%%, 期望 0%%", rep.FalsePositiveRate*100)
	}
	if len(rep.FalsePositiveMarks) != 0 {
		t.Errorf("误报标记 = %v, 期望为空", rep.FalsePositiveMarks)
	}
	if rep.TotalMarks != 10 {
		t.Errorf("标记总数 = %d, 期望 10", rep.TotalMarks)
	}
}

func TestAcceptanceDetectionTimeDistribution(t *testing.T) {
	rep := runAcceptance(t)
	counts := map[float64]int{}
	crashes := 0
	for _, d := range rep.Detections {
		if d.Event.Kind != CrashEvent {
			continue
		}
		crashes++
		if d.FirstSuspectedAt < 0 {
			t.Errorf("宕机事件 %d 缺少首次怀疑时刻", d.Event.ID)
		}
		counts[d.MTTD()]++
	}
	if crashes != 10 {
		t.Fatalf("宕机事件数 = %d, 期望 10", crashes)
	}
	if counts[2] != 6 || counts[3] != 4 {
		t.Errorf("MTTD 分布 = %v, 期望 {2:6, 3:4}", counts)
	}
}

func TestAcceptanceDisturbancesNotMarked(t *testing.T) {
	rep := runAcceptance(t)
	disturbances := 0
	for _, d := range rep.Detections {
		if d.Event.Kind != DisturbanceEvent {
			continue
		}
		disturbances++
		if d.Detected() {
			t.Errorf("扰动事件 %d 被误标记于周期 %d", d.Event.ID, d.MarkedAt)
		}
		if d.FirstSuspectedAt < 0 {
			t.Errorf("扰动事件 %d 未触发怀疑", d.Event.ID)
		}
		if !d.Cleared {
			t.Errorf("扰动事件 %d 的怀疑未被澄清", d.Event.ID)
		}
	}
	if disturbances != 5 {
		t.Fatalf("扰动事件数 = %d, 期望 5", disturbances)
	}
}

func TestAnalyzerFalsePositive(t *testing.T) {
	a := NewAnalyzer()
	a.RegisterEvent(FaultEvent{ID: 0, Kind: DisturbanceEvent, NodeID: 3, OccurredAt: 5})
	a.RecordObservation(Observation{Tick: 5, NodeID: 3, Kind: ObsSuspect})
	a.RecordObservation(Observation{Tick: 7, NodeID: 3, Kind: ObsMark})

	rep := a.Analyze()
	if len(rep.FalsePositiveMarks) != 1 || rep.FalsePositiveMarks[0] != 7 {
		t.Fatalf("误报标记 = %v, 期望 [7]", rep.FalsePositiveMarks)
	}
	if rep.FalsePositiveRate != 1.0 {
		t.Errorf("误报率 = %.2f, 期望 1.0", rep.FalsePositiveRate)
	}
}

func TestAnalyzerUnmatchedMarkIsFalsePositive(t *testing.T) {
	a := NewAnalyzer()
	a.RegisterEvent(FaultEvent{ID: 0, Kind: CrashEvent, NodeID: 1, OccurredAt: 10})
	a.RecordObservation(Observation{Tick: 12, NodeID: 1, Kind: ObsMark})
	a.RecordObservation(Observation{Tick: 30, NodeID: 2, Kind: ObsMark}) // 无任何事件对应

	rep := a.Analyze()
	if rep.TotalMarks != 2 {
		t.Fatalf("标记总数 = %d, 期望 2", rep.TotalMarks)
	}
	if len(rep.FalsePositiveMarks) != 1 || rep.FalsePositiveMarks[0] != 30 {
		t.Errorf("误报标记 = %v, 期望 [30]", rep.FalsePositiveMarks)
	}
	if math.Abs(rep.FalsePositiveRate-0.5) > 1e-9 {
		t.Errorf("误报率 = %.4f, 期望 0.5", rep.FalsePositiveRate)
	}
	if math.Abs(rep.AvgMTTD-2) > 1e-9 {
		t.Errorf("平均 MTTD = %.2f, 期望 2", rep.AvgMTTD)
	}
}

func TestAnalyzerMissedCrash(t *testing.T) {
	a := NewAnalyzer()
	a.RegisterEvent(FaultEvent{ID: 0, Kind: CrashEvent, NodeID: 1, OccurredAt: 10})

	rep := a.Analyze()
	if rep.MissedCrashes != 1 {
		t.Errorf("漏报数 = %d, 期望 1", rep.MissedCrashes)
	}
	if rep.DetectedCrashes != 0 {
		t.Errorf("成功检测数 = %d, 期望 0", rep.DetectedCrashes)
	}
}

func TestAnalyzerMTTDMath(t *testing.T) {
	a := NewAnalyzer()
	a.RegisterEvent(FaultEvent{ID: 0, Kind: CrashEvent, NodeID: 1, OccurredAt: 10})
	a.RegisterEvent(FaultEvent{ID: 1, Kind: CrashEvent, NodeID: 2, OccurredAt: 20})
	a.RecordObservation(Observation{Tick: 12, NodeID: 1, Kind: ObsMark})
	a.RecordObservation(Observation{Tick: 24, NodeID: 2, Kind: ObsMark})

	rep := a.Analyze()
	if math.Abs(rep.AvgMTTD-3.0) > 1e-9 {
		t.Errorf("平均 MTTD = %.4f, 期望 3.0", rep.AvgMTTD)
	}
}

func TestReportContent(t *testing.T) {
	rep := runAcceptance(t)
	s := rep.String()
	for _, want := range []string{"SWIM 故障检测诊断报告", "MTTD", "2.4", "误报率: 0.0%", "节点宕机", "网络丢包"} {
		if !strings.Contains(s, want) {
			t.Errorf("报告缺少内容 %q\n报告全文:\n%s", want, s)
		}
	}
}
