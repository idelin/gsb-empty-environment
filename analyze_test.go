package main

import (
	"testing"
	"time"
)

func analyzeTestEvents(t *testing.T, events []Event) Report {
	t.Helper()
	traces, errs := Replay(events)
	if len(errs) != 0 {
		t.Fatalf("replay returned errors: %v", errs)
	}
	return AnalyzeTrace("t", traces["t"])
}

func spanEvents(rows [][4]string) []Event {
	var events []Event
	at := int64(1)
	for _, row := range rows {
		id, kind, refType, parent := row[0], row[1], row[2], row[3]
		if kind == "span" {
			events = append(events,
				Event{At: at, TraceID: "t", SpanID: id, Kind: EventStart, Start: 0},
				Event{At: at + 1, TraceID: "t", SpanID: id, Kind: EventEnd, End: 0},
			)
			at += 2
		} else {
			events = append(events, Event{
				At:           at,
				TraceID:      "t",
				SpanID:       id,
				Kind:         EventReference,
				RefType:      refType,
				ParentSpanID: parent,
			})
			at++
		}
	}
	return events
}

func withInterval(events []Event, id string, start, end time.Duration) []Event {
	startNS, endNS := int64(start/time.Nanosecond), int64(end/time.Nanosecond)
	for i := range events {
		if events[i].SpanID != id {
			continue
		}
		if events[i].Kind == EventStart {
			events[i].Start = startNS
		}
		if events[i].Kind == EventEnd {
			events[i].End = endNS
		}
	}
	return events
}

func problemCodes(report Report) map[string]bool {
	codes := make(map[string]bool)
	for _, problem := range report.Problems {
		codes[problem.Code] = true
	}
	return codes
}

func TestParallelChildrenDoNotDoubleCountParentWall(t *testing.T) {
	events := spanEvents([][4]string{
		{"root", "span", "", ""},
		{"left", "span", "", ""},
		{"right", "span", "", ""},
		{"left", "ref", RefChildOf, "root"},
		{"right", "ref", RefChildOf, "root"},
	})
	events = withInterval(events, "root", 0, 10*time.Millisecond)
	events = withInterval(events, "left", 0, 10*time.Millisecond)
	events = withInterval(events, "right", 0, 10*time.Millisecond)

	report := analyzeTestEvents(t, events)

	if report.TimelineWall != 10*time.Millisecond {
		t.Fatalf("timeline wall = %s, want 10ms", report.TimelineWall)
	}
	if report.Critical.Wall != 10*time.Millisecond {
		t.Fatalf("critical wall = %s, want 10ms", report.Critical.Wall)
	}
	if report.Critical.Work != 10*time.Millisecond {
		t.Fatalf("critical work = %s, want one 10ms branch", report.Critical.Work)
	}
	if report.TotalWork != 20*time.Millisecond {
		t.Fatalf("accumulated work = %s, want both parallel branches once", report.TotalWork)
	}
	if len(report.Critical.SpanIDs) != 2 {
		t.Fatalf("critical path = %v, want one parent and one branch", report.Critical.SpanIDs)
	}
}

func TestAsyncWaitAndSharedSuccessorCountedOnce(t *testing.T) {
	events := spanEvents([][4]string{
		{"source", "span", "", ""},
		{"left", "span", "", ""},
		{"right", "span", "", ""},
		{"join", "span", "", ""},
		{"left", "ref", RefChildOf, "source"},
		{"right", "ref", RefChildOf, "source"},
		{"join", "ref", RefFollowsFrom, "left"},
		{"join", "ref", RefFollowsFrom, "right"},
	})
	events = withInterval(events, "source", 0, 10*time.Millisecond)
	events = withInterval(events, "left", 5*time.Millisecond, 10*time.Millisecond)
	events = withInterval(events, "right", 5*time.Millisecond, 10*time.Millisecond)
	events = withInterval(events, "join", 20*time.Millisecond, 25*time.Millisecond)

	report := analyzeTestEvents(t, events)

	if report.TimelineWall != 25*time.Millisecond {
		t.Fatalf("timeline wall = %s, want 25ms including async wait", report.TimelineWall)
	}
	if report.Critical.Wall != 25*time.Millisecond {
		t.Fatalf("critical wall = %s, want 25ms", report.Critical.Wall)
	}
	if report.Critical.Work != 15*time.Millisecond {
		t.Fatalf("critical work = %s, want source+one branch+join once", report.Critical.Work)
	}
	if report.Critical.IdleWaiting != 10*time.Millisecond {
		t.Fatalf("async wait = %s, want 10ms", report.Critical.IdleWaiting)
	}
	joinCount := 0
	for _, id := range report.Critical.SpanIDs {
		if id == "join" {
			joinCount++
		}
	}
	if joinCount != 1 {
		t.Fatalf("path %v contains join %d times, want once", report.Critical.SpanIDs, joinCount)
	}
}

func TestNegativeDurationReportedAndExcluded(t *testing.T) {
	events := spanEvents([][4]string{
		{"good", "span", "", ""},
		{"bad", "span", "", ""},
	})
	events = withInterval(events, "good", 0, 7*time.Millisecond)
	events = withInterval(events, "bad", 9*time.Millisecond, 2*time.Millisecond)

	report := analyzeTestEvents(t, events)

	if !problemCodes(report)["NEGATIVE_DURATION"] {
		t.Fatalf("problems = %v, want NEGATIVE_DURATION", report.Problems)
	}
	if report.ValidSpans != 1 || report.TimelineWall != 7*time.Millisecond || report.TotalWork != 7*time.Millisecond {
		t.Fatalf("negative span included in metrics: %+v", report)
	}
	if report.Critical.SpanIDs[0] != "good" {
		t.Fatalf("critical path = %v, want good span", report.Critical.SpanIDs)
	}
}

func TestCausalViolationDoesNotCreateCriticalPath(t *testing.T) {
	events := spanEvents([][4]string{
		{"producer", "span", "", ""},
		{"consumer", "span", "", ""},
		{"consumer", "ref", RefFollowsFrom, "producer"},
	})
	events = withInterval(events, "producer", 0, 10*time.Millisecond)
	events = withInterval(events, "consumer", 5*time.Millisecond, 15*time.Millisecond)

	report := analyzeTestEvents(t, events)

	if !problemCodes(report)["CAUSAL_VIOLATION"] {
		t.Fatalf("problems = %v, want CAUSAL_VIOLATION", report.Problems)
	}
	if report.Critical.Wall != 10*time.Millisecond {
		t.Fatalf("critical wall = %s, invalid edge must not make a 15ms pseudo-path", report.Critical.Wall)
	}
	if report.TimelineWall != 15*time.Millisecond {
		t.Fatalf("observed timeline wall = %s, want 15ms", report.TimelineWall)
	}
	if report.TotalWork != 20*time.Millisecond {
		t.Fatalf("work = %s, want two valid 10ms spans", report.TotalWork)
	}
}

func TestCycleReportedWithoutPseudoPath(t *testing.T) {
	events := spanEvents([][4]string{
		{"alpha", "span", "", ""},
		{"beta", "span", "", ""},
		{"gamma", "span", "", ""},
		{"beta", "ref", RefFollowsFrom, "alpha"},
		{"gamma", "ref", RefFollowsFrom, "beta"},
		{"alpha", "ref", RefFollowsFrom, "gamma"},
	})
	events = withInterval(events, "alpha", 0, 0)
	events = withInterval(events, "beta", 0, 0)
	events = withInterval(events, "gamma", 0, 0)

	report := analyzeTestEvents(t, events)

	if report.Status != statusCycle || !problemCodes(report)["CYCLE_DETECTED"] {
		t.Fatalf("status=%s problems=%v, want cycle report", report.Status, report.Problems)
	}
	if len(report.Critical.SpanIDs) != 0 || report.Critical.Wall != 0 || report.Critical.Work != 0 {
		t.Fatalf("cycle emitted pseudo critical path: %+v", report.Critical)
	}
}

func TestReplayToleratesLateReferenceDelivery(t *testing.T) {
	events := []Event{
		{At: 1, TraceID: "t", SpanID: "child", Kind: EventReference, RefType: RefChildOf, ParentSpanID: "root"},
		{At: 2, TraceID: "t", SpanID: "root", Kind: EventStart, Start: 0},
		{At: 3, TraceID: "t", SpanID: "child", Kind: EventStart, Start: 0},
		{At: 4, TraceID: "t", SpanID: "child", Kind: EventEnd, End: int64(time.Millisecond)},
		{At: 5, TraceID: "t", SpanID: "root", Kind: EventEnd, End: int64(time.Millisecond)},
	}

	report := analyzeTestEvents(t, events)
	if len(report.Problems) != 0 {
		t.Fatalf("late reference caused problems: %v", report.Problems)
	}
	if got := report.Critical.Wall; got != time.Millisecond {
		t.Fatalf("critical wall = %s, want 1ms", got)
	}
}

func TestReplayRejectsIncompleteAndUnknownEvents(t *testing.T) {
	tests := []struct {
		name  string
		event Event
	}{
		{"missing trace", Event{At: 1, SpanID: "x", Kind: EventStart}},
		{"missing span", Event{At: 1, TraceID: "t", Kind: EventStart}},
		{"unknown kind", Event{At: 1, TraceID: "t", SpanID: "x", Kind: "mystery"}},
		{"unknown ref", Event{At: 1, TraceID: "t", SpanID: "x", Kind: EventReference, RefType: "link", ParentSpanID: "y"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, errs := Replay([]Event{test.event}); len(errs) == 0 {
				t.Fatal("want replay error, got nil")
			}
		})
	}
}

func TestSampleFixture(t *testing.T) {
	traces, errs := Replay(SampleEvents())
	if len(errs) != 0 {
		t.Fatalf("sample fixture replay errors: %v", errs)
	}

	good := AnalyzeTrace(TraceGood, traces[TraceGood])
	if good.TimelineWall != 22*time.Millisecond || good.TotalWork != 28*time.Millisecond {
		t.Fatalf("good metrics = wall %s work %s", good.TimelineWall, good.TotalWork)
	}
	if good.Critical.Wall != 22*time.Millisecond || good.Critical.Work != 18*time.Millisecond || good.Critical.IdleWaiting != 4*time.Millisecond {
		t.Fatalf("good critical = %+v", good.Critical)
	}

	invalid := AnalyzeTrace(TraceInvalid, traces[TraceInvalid])
	codes := problemCodes(invalid)
	for _, code := range []string{"NEGATIVE_DURATION", "CAUSAL_VIOLATION", "UNRESOLVED_REFERENCE"} {
		if !codes[code] {
			t.Fatalf("invalid problems = %v, want %s", invalid.Problems, code)
		}
	}
	if invalid.Critical.Wall != 10*time.Millisecond {
		t.Fatalf("invalid critical wall = %s, want edge excluded", invalid.Critical.Wall)
	}

	cycle := AnalyzeTrace(TraceCycle, traces[TraceCycle])
	if cycle.Status != statusCycle || len(cycle.Critical.SpanIDs) != 0 {
		t.Fatalf("cycle report = %+v", cycle)
	}
}
