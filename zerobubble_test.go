package zerobubble_test

import (
	"fmt"
	"strings"
	"testing"

	zb "prescreen.local/ps-53e209913c"
)

func cfg2x4() zb.Config {
	return zb.Config{
		P: 2, M: 4,
		TF: 1, TB: 1, TW: 1, TComm: 0,
		MemF: 3, MemW: 1,
		MemoryLimits: []int64{6},
	}
}

func cfg4x8() zb.Config {
	return zb.Config{
		P: 4, M: 8,
		TF: 1, TB: 1, TW: 1, TComm: 0.1,
		MemF: 3, MemW: 1,
		MemoryLimits: []int64{12},
	}
}

func TestZeroBubbleDependenciesAndResourceBounds(t *testing.T) {
	cfg := cfg4x8()
	s, err := zb.BuildZeroBubble(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 3*cfg.P*cfg.M {
		t.Fatalf("got %d tasks, want %d", len(s.Tasks), 3*cfg.P*cfg.M)
	}
	if want := 2 * (cfg.P - 1) * cfg.M; len(s.Comms) != want {
		t.Fatalf("got %d messages, want %d", len(s.Comms), want)
	}

	report := zb.Audit(s)
	if !report.OK {
		t.Fatalf("audit failed:\n%s", report.Error())
	}
	for _, ok := range []bool{
		report.DependencyOK, report.ResourceOK, report.MemoryOK,
		report.CommunicationOK, report.AcyclicOK, report.DeadlockFree,
	} {
		if !ok {
			t.Fatalf("audit flags not all true: %+v", report)
		}
	}
	for stage := 0; stage < cfg.P; stage++ {
		if report.PeakMemory[stage] > cfg.MemoryLimits[0] {
			t.Fatalf("stage %d peak %d > limit %d", stage, report.PeakMemory[stage], cfg.MemoryLimits[0])
		}
		if report.PeakMemory[stage] != s.PeakMemory[stage] {
			t.Fatalf("stage %d reported peak %d, schedule peak %d", stage, report.PeakMemory[stage], s.PeakMemory[stage])
		}
	}
}

func TestTightMemoryForcesEarliestWeightBeforeNextForward(t *testing.T) {
	cfg := cfg2x4()
	cfg.MemF = 3
	cfg.MemW = 2
	cfg.MemoryLimits = []int64{5}

	s, err := zb.BuildZeroBubble(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report := zb.Audit(s)
	if !report.OK {
		t.Fatalf("tight-memory audit failed: %s", report.Error())
	}
	if report.PeakMemory[0] != 5 || report.PeakMemory[1] > 5 {
		t.Fatalf("unexpected peak memory: %v", report.PeakMemory)
	}

	w0, _ := s.Task(zb.Weight, 0, 0)
	f2, _ := s.Task(zb.Forward, 2, 0)
	if w0.Start >= f2.Start {
		t.Fatalf("W(0,0) [%.3f,%.3f) was not forced before F(2,0) at %.3f",
			w0.Start, w0.End, f2.Start)
	}
	if w0.End > f2.Start+1e-9 {
		t.Fatalf("F(2,0) starts before W(0,0) releases memory")
	}
}

func TestWeightMemoryLargerThanActivationMemoryIsStillSafe(t *testing.T) {
	cfg := zb.Config{
		P: 3, M: 5,
		TF: 1, TB: 1, TW: 1, TComm: 0.1,
		MemF: 2, MemW: 5,
		MemoryLimits: []int64{5},
	}
	s, err := zb.BuildZeroBubble(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report := zb.Audit(s)
	if !report.OK {
		t.Fatalf("audit failed: %s", report.Error())
	}
	for stage, peak := range report.PeakMemory {
		if peak > 5 {
			t.Fatalf("stage %d peak %d > 5", stage, peak)
		}
	}
}

func TestStrictBubbleImprovementOver1F1B(t *testing.T) {
	cases := []struct {
		name string
		cfg  zb.Config
	}{
		{"2x4-no-comm", cfg2x4()},
		{"3x6", zb.Config{P: 3, M: 6, TF: 1, TB: 1, TW: 1, TComm: 0.05, MemF: 3, MemW: 1, MemoryLimits: []int64{10}}},
		{"4x8-comm", cfg4x8()},
		{"8x16-skewed", zb.Config{P: 8, M: 16, TF: 1, TB: 1.5, TW: 0.5, TComm: 0.05, MemF: 5, MemW: 1, MemoryLimits: []int64{24}}},
		{"2x12-large-w", zb.Config{P: 2, M: 12, TF: 1, TB: 0.5, TW: 2, TComm: 0.1, MemF: 4, MemW: 2, MemoryLimits: []int64{24}}},
		{"4x20-large-w", zb.Config{P: 4, M: 20, TF: 1, TB: 0.5, TW: 2, TComm: 0.1, MemF: 4, MemW: 2, MemoryLimits: []int64{24}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmp, err := zb.Compare(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !cmp.StrictlyBetter() {
				t.Fatalf("expected strict improvement, got baseline %.3f/%0.3f%%, zb %.3f/%0.3f%%",
					cmp.Baseline.Makespan, 100*cmp.BaselineBubbleRate,
					cmp.ZeroBubble.Makespan, 100*cmp.ZeroBubbleBubbleRate)
			}
			if cmp.BubbleRateImprovement <= 0 {
				t.Fatalf("non-positive bubble improvement: %v", cmp.BubbleRateImprovement)
			}
			if err := cmp.Baseline.Validate(); err != nil {
				t.Fatalf("baseline invalid: %v", err)
			}
			if err := cmp.ZeroBubble.Validate(); err != nil {
				t.Fatalf("zero-bubble invalid: %v", err)
			}
			if !strings.Contains(cmp.Report(), "strictly-better=true") {
				t.Fatalf("report missing strict result:\n%s", cmp.Report())
			}
		})
	}
}

func TestNonUniformStageCosts(t *testing.T) {
	cfg := zb.Config{
		P: 3, M: 5,
		TF: 1, TB: 1, TW: 1, TComm: 0.1,
		MemF: 3, MemW: 1,
		MemoryLimits: []int64{9, 10, 12},
		StageCosts: []zb.StageCost{
			{TF: 1.2, TB: 1.0, TW: 0.8, MemF: 3, MemW: 1},
			{TF: 0.9, TB: 1.4, TW: 1.1, MemF: 3, MemW: 2},
			{TF: 1.1, TB: 0.8, TW: 0.7, MemF: 2, MemW: 1},
		},
	}
	cmp, err := zb.Compare(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmp.ZeroBubble.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cmp.StrictlyBetter() {
		t.Fatalf("non-uniform config did not improve: %s", cmp.Report())
	}
	report := zb.Audit(cmp.ZeroBubble)
	for stage, limit := range cfg.MemoryLimits {
		if report.PeakMemory[stage] > limit {
			t.Fatalf("stage %d peak %d > %d", stage, report.PeakMemory[stage], limit)
		}
	}
}

func TestCommunicationPairsFIFOAndBidirectionalChecks(t *testing.T) {
	cfg := zb.Config{
		P: 5, M: 7,
		TF: 1, TB: 1.2, TW: 0.8, TComm: 0.2,
		MemF: 4, MemW: 2,
		MemoryLimits: []int64{16},
	}
	s, err := zb.BuildZeroBubble(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report := zb.CheckCommunication(s)
	if report == nil || !report.OK || !report.DeadlockFree || !report.FIFOOrderOK || !report.PairedOK {
		t.Fatalf("bad communication report: %+v", report)
	}
	if report.Messages != 2*(cfg.P-1)*cfg.M {
		t.Fatalf("messages=%d", report.Messages)
	}
	for link := 0; link+1 < cfg.P; link++ {
		seenForward := make(map[int]bool)
		seenBackward := make(map[int]bool)
		for _, c := range s.Comms {
			if c.FromStage == link && c.ToStage == link+1 {
				seenForward[c.Microbatch] = true
			}
			if c.FromStage == link+1 && c.ToStage == link {
				seenBackward[c.Microbatch] = true
			}
			if !nearly(c.SendStart, c.RecvStart) || !nearly(c.SendEnd, c.RecvEnd) {
				t.Fatalf("non-rendezvous message %s: %+v", c.ID, c)
			}
		}
		if len(seenForward) != cfg.M || len(seenBackward) != cfg.M {
			t.Fatalf("link %d incomplete: forward=%d backward=%d", link, len(seenForward), len(seenBackward))
		}
	}
}

func TestAuditRejectsBrokenDependency(t *testing.T) {
	s, err := zb.BuildZeroBubble(cfg2x4())
	if err != nil {
		t.Fatal(err)
	}
	for i := range s.Tasks {
		if s.Tasks[i].ID.Kind == zb.Weight {
			s.Tasks[i].Deps = nil
			break
		}
	}
	report := zb.Audit(s)
	if report.OK || report.DependencyOK {
		t.Fatalf("broken dependency was accepted: %+v", report)
	}
	if !strings.Contains(report.Error().Error(), "dependency") {
		t.Fatalf("unexpected error: %v", report.Error())
	}
}

func TestAuditRejectsMemoryOverflow(t *testing.T) {
	s, err := zb.BuildZeroBubble(cfg4x8())
	if err != nil {
		t.Fatal(err)
	}
	s.Config.MemoryLimits = []int64{4}
	report := zb.Audit(s)
	if report.OK || report.MemoryOK {
		t.Fatalf("memory overflow was accepted: %+v", report)
	}
	if !strings.Contains(report.Error().Error(), "memory") {
		t.Fatalf("unexpected error: %v", report.Error())
	}
}

func TestAuditRejectsCommunicationMismatch(t *testing.T) {
	s, err := zb.BuildZeroBubble(cfg4x8())
	if err != nil {
		t.Fatal(err)
	}
	s.Comms[0].RecvStart += 1
	s.Comms[0].RecvEnd += 1
	report := zb.Audit(s)
	if report.OK || report.CommunicationOK || report.DeadlockFree {
		t.Fatalf("communication mismatch was accepted: %+v", report)
	}
}

func TestGanttContainsExactTaskIntervals(t *testing.T) {
	s, err := zb.BuildZeroBubble(cfg2x4())
	if err != nil {
		t.Fatal(err)
	}
	text := s.Gantt()
	f0, _ := s.Task(zb.Forward, 0, 0)
	want := fmt.Sprintf("%5d | F0   | %5.3f | %3.3f", 0, f0.Start, f0.End)
	if !strings.Contains(text, want) {
		t.Fatalf("Gantt missing %q:\n%s", want, text)
	}
	if !strings.Contains(text, "S0 ") || !strings.Contains(text, "S1 ") {
		t.Fatalf("Gantt missing stage timelines:\n%s", text)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := cfg2x4()
	if _, err := valid.Resolve(); err != nil {
		t.Fatal(err)
	}
	alias := valid
	alias.MemLimit = alias.MemoryLimits
	alias.MemoryLimits = nil
	if _, err := alias.Resolve(); err != nil {
		t.Fatalf("MemLimit alias rejected: %v", err)
	}
	bad := valid
	bad.MemoryLimits = []int64{1}
	if _, err := bad.Resolve(); err == nil {
		t.Fatal("expected memory limit rejection")
	}
	bad = valid
	bad.StageCosts = []zb.StageCost{{TF: 0, TB: 1, TW: 1}}
	if _, err := bad.Resolve(); err == nil {
		t.Fatal("expected bad StageCosts length rejection")
	}
}

func nearly(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1e-9
}
