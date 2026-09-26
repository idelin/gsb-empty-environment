package trace

import (
	"reflect"
	"testing"
)

// Two parallel 10ms children must not inflate the parent's
// wall-clock to 20ms; accumulated work still counts both.
func TestParallelChildrenWallClockVsWork(t *testing.T) {
	tr := &Trace{
		ID: "parallel",
		Spans: []Span{
			{ID: "root", Start: 0, End: 10},
			{ID: "a", Parent: "root", Start: 0, End: 10},
			{ID: "b", Parent: "root", Start: 0, End: 10},
		},
	}
	r := Analyze(tr)
	if r.WallClock != 10 {
		t.Errorf("wall-clock = %dms, want 10ms", r.WallClock)
	}
	if r.TotalWork != 30 {
		t.Errorf("total work = %dms, want 30ms", r.TotalWork)
	}
	if r.CriticalPath == nil {
		t.Fatal("critical path missing")
	}
	if r.CriticalPath.WallClock != 10 {
		t.Errorf("critical path wall-clock = %dms, want 10ms", r.CriticalPath.WallClock)
	}
	if len(r.Cycles) != 0 || len(r.NegativeDurations) != 0 || len(r.CausalityViolations) != 0 {
		t.Errorf("unexpected anomalies: %+v", r)
	}
}

// A successor shared by two predecessors is counted once in the
// accumulated work and appears once on the critical path.
func TestSharedSuccessorCountedOnce(t *testing.T) {
	tr := &Trace{
		ID: "diamond",
		Spans: []Span{
			{ID: "root", Start: 0, End: 30},
			{ID: "a", Parent: "root", Start: 0, End: 10},
			{ID: "b", Parent: "root", Start: 10, End: 20},
			{ID: "join", Parent: "root", Start: 20, End: 30},
		},
		Dependencies: []Dependency{
			{From: "a", To: "join"},
			{From: "b", To: "join"},
		},
	}
	r := Analyze(tr)
	if r.TotalWork != 30+10+10+10 {
		t.Errorf("total work = %dms, want 60ms (join counted once)", r.TotalWork)
	}
	count := 0
	for _, id := range r.CriticalPath.Spans {
		if id == "join" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("join appears %d times on critical path, want 1: %v", count, r.CriticalPath.Spans)
	}
}

// Async dependencies extend the causal chain across idle gaps;
// wall-clock includes the gap, work does not.
func TestAsyncDependencyChain(t *testing.T) {
	tr := &Trace{
		ID: "async",
		Spans: []Span{
			{ID: "root", Start: 0, End: 100},
			{ID: "enqueue", Parent: "root", Start: 5, End: 15},
			{ID: "worker", Parent: "root", Start: 40, End: 70},
			{ID: "notify", Parent: "root", Start: 70, End: 90},
		},
		Dependencies: []Dependency{
			{From: "enqueue", To: "worker"},
			{From: "worker", To: "notify"},
		},
	}
	r := Analyze(tr)
	if r.CriticalPath == nil {
		t.Fatal("critical path missing")
	}
	if r.CriticalPath.WallClock != 100 {
		t.Errorf("critical path wall-clock = %dms, want 100ms", r.CriticalPath.WallClock)
	}
	want := []string{"root", "enqueue", "worker", "notify"}
	if !reflect.DeepEqual(r.CriticalPath.Spans, want) {
		t.Errorf("critical path = %v, want %v", r.CriticalPath.Spans, want)
	}
	if r.CriticalPath.Work > r.CriticalPath.WallClock {
		t.Errorf("work %dms exceeds wall-clock %dms on one chain",
			r.CriticalPath.Work, r.CriticalPath.WallClock)
	}
}

// Cyclic dependencies are reported and yield no fabricated path.
// Zero-duration spans form a logically cyclic but temporally
// consistent graph, so the cycle reaches the detector intact.
func TestCycleReportedNoFakePath(t *testing.T) {
	tr := &Trace{
		ID: "cycle",
		Spans: []Span{
			{ID: "a", Start: 0, End: 0},
			{ID: "b", Start: 0, End: 0},
			{ID: "c", Start: 0, End: 0},
		},
		Dependencies: []Dependency{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
			{From: "c", To: "a"},
		},
	}
	r := Analyze(tr)
	if r.CriticalPath != nil {
		t.Errorf("critical path = %v, want nil on cyclic graph", r.CriticalPath.Spans)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(r.Cycles, want) {
		t.Errorf("cycles = %v, want %v", r.Cycles, want)
	}
}

// Negative durations and causality violations are reported in
// separate categories and excluded from the graph.
func TestAnomaliesReportedSeparately(t *testing.T) {
	tr := &Trace{
		ID: "anomalies",
		Spans: []Span{
			{ID: "root", Start: 0, End: 50},
			{ID: "neg", Parent: "root", Start: 30, End: 20},
			{ID: "escapee", Parent: "root", Start: 10, End: 60},
			{ID: "a", Parent: "root", Start: 5, End: 15},
			{ID: "b", Parent: "root", Start: 10, End: 20},
		},
		Dependencies: []Dependency{
			{From: "a", To: "b"}, // b starts before a ends: violated
		},
	}
	r := Analyze(tr)
	if !reflect.DeepEqual(r.NegativeDurations, []string{"neg"}) {
		t.Errorf("negative durations = %v, want [neg]", r.NegativeDurations)
	}
	if len(r.CausalityViolations) != 2 {
		t.Fatalf("causality violations = %v, want 2 entries", r.CausalityViolations)
	}
	// Violating edges are dropped but the spans themselves stay in
	// the graph, so the escapee still extends the wall clock.
	if r.WallClock != 60 {
		t.Errorf("wall-clock = %dms, want 60ms", r.WallClock)
	}
	if r.TotalWork != 50+50+10+10 {
		t.Errorf("total work = %dms, want 120ms (only neg excluded)", r.TotalWork)
	}
}

// Empty and degenerate traces must not panic.
func TestDegenerateTraces(t *testing.T) {
	r := Analyze(&Trace{ID: "empty"})
	if r.WallClock != 0 || r.TotalWork != 0 || r.CriticalPath != nil {
		t.Errorf("empty trace: %+v", r)
	}
	r = Analyze(&Trace{ID: "one", Spans: []Span{{ID: "only", Start: 3, End: 7}}})
	if r.WallClock != 4 || r.TotalWork != 4 {
		t.Errorf("single span: wall %d work %d, want 4/4", r.WallClock, r.TotalWork)
	}
	if r.CriticalPath == nil || !reflect.DeepEqual(r.CriticalPath.Spans, []string{"only"}) {
		t.Errorf("single span critical path: %+v", r.CriticalPath)
	}
}
