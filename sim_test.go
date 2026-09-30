package main

import (
	"math"
	"reflect"
	"testing"
)

// Serialization delay is exactly ceil(bytes/rate) ticks on every link.
func TestSerializationTicks(t *testing.T) {
	cfg := testConfig()
	res := RunSim(cfg)
	rate := map[string]int64{
		"L1":  int64(cfg.Rate1),
		"L2A": int64(cfg.Rate2A),
		"L2B": int64(cfg.Rate2B),
	}
	byID := map[int]Event{}
	for _, e := range res.Trace.Events() {
		byID[e.ID] = e
	}
	seen := 0
	for _, e := range res.Trace.Events() {
		if e.Kind != KindSendEnd {
			continue
		}
		if len(e.Parents) != 1 {
			t.Fatalf("send_end %d has %d parents, want 1", e.ID, len(e.Parents))
		}
		ss := byID[e.Parents[0]]
		if ss.Kind != KindSendStart {
			t.Fatalf("send_end parent %d is %v", e.ID, ss.Kind)
		}
		want := int64(math.Ceil(float64(cfg.PacketBytes) / float64(rate[e.Link])))
		if got := e.Tick - ss.Tick; got != want {
			t.Fatalf("link %s: serialization %d ticks, want %d", e.Link, got, want)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("no transmissions observed")
	}
}

// The discrete-event model is deterministic: identical config, identical trace.
func TestDeterministic(t *testing.T) {
	a, b := RunSim(testConfig()), RunSim(testConfig())
	ea, eb := a.Trace.Events(), b.Trace.Events()
	if len(ea) != len(eb) {
		t.Fatalf("event counts differ: %d vs %d", len(ea), len(eb))
	}
	for i := range ea {
		if !reflect.DeepEqual(ea[i], eb[i]) {
			t.Fatalf("event %d differs: %+v vs %+v", i, ea[i], eb[i])
		}
	}
}

// Losses trigger timeouts and retransmissions; all packets still arrive.
func TestRetransmissionOccurs(t *testing.T) {
	res := RunSim(testConfig())
	timeouts, reenqueues := 0, 0
	for _, e := range res.Trace.Events() {
		switch e.Kind {
		case KindTimeout:
			timeouts++
		case KindEnqueue:
			if e.Attempt > 0 {
				reenqueues++
			}
		}
	}
	if timeouts == 0 || reenqueues == 0 {
		t.Fatalf("timeouts=%d reenqueues=%d, want both > 0", timeouts, reenqueues)
	}
	if len(res.Delivered) != testConfig().NumPackets {
		t.Fatalf("delivered %d, want %d", len(res.Delivered), testConfig().NumPackets)
	}
	foundRetransDelay := false
	for _, b := range AnalyzePackets(res.Trace) {
		if b.RetransWait > 0 {
			foundRetransDelay = true
		}
	}
	if !foundRetransDelay {
		t.Fatal("no packet shows retransmission wait")
	}
}

// DAG metrics on the simulated trace: DP equals exhaustive enumeration,
// work bounds the critical path, and the trace is conflict-free.
func TestDAGMetricsOnSimTrace(t *testing.T) {
	res := RunSim(testConfig())
	cp, path := CriticalPath(res.Trace)
	work := TotalWork(res.Trace)
	best, _, ok := EnumerateCriticalPath(res.Trace, 1_000_000)
	if !ok {
		t.Fatal("simulation DAG too large to enumerate")
	}
	if best != cp {
		t.Fatalf("enumerate=%d != DP=%d", best, cp)
	}
	if work < cp {
		t.Fatalf("work %d < critical path %d", work, cp)
	}
	if len(path) < 2 {
		t.Fatalf("critical path too short: %v", path)
	}
	if cp > res.Makespan {
		t.Fatalf("critical path %d exceeds makespan %d", cp, res.Makespan)
	}
	conflicts, missing := CheckTrace(res.Trace)
	if len(conflicts) != 0 || len(missing) != 0 {
		t.Fatalf("conflicts=%v missing=%v", conflicts, missing)
	}
}
