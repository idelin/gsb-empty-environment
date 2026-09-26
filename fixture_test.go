package tailsampler

import (
	"os"
	"testing"
	"time"
)

func TestFixtureReplay(t *testing.T) {
	f, err := os.Open("testdata/fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	cfg := Config{
		TraceTimeout:     5 * time.Second,
		DecisionTTL:      time.Minute,
		BudgetPerSecond:  100,
		LatencyThreshold: 2 * time.Second,
	}
	results, err := Replay(f, cfg, testStart)
	if err != nil {
		t.Fatal(err)
	}

	want := []ReplayResult{
		{AtMS: 0, TraceID: "a", Decision: drop("no_signal")},
		{AtMS: 200, TraceID: "b", Decision: keep("error")},
		{AtMS: 300, TraceID: "a", Decision: drop("no_signal")}, // late span, same decision
		{AtMS: 500, TraceID: "c", Decision: keep("slow_trace")},
		{AtMS: 6000, TraceID: "d", Decision: drop("timeout")}, // timeout sweep
		{AtMS: 6100, TraceID: "d", Decision: drop("timeout")}, // late span, same decision
	}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(results), len(want), results)
	}
	for i, w := range want {
		if results[i] != w {
			t.Errorf("result %d: got %+v, want %+v", i, results[i], w)
		}
	}
}
