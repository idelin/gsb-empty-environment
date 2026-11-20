package keyepoch

import "testing"

// TestEnumerationInvariants enumerates the bounded state space of a single
// engine over small queues and checks every reached state against the
// key-generation, send-count, delivery and independent-replay invariants. The
// action set deliberately covers simultaneous updates, cancel-before-accept,
// post-commit disconnects, post-close stale callbacks and cross-generation
// sequence reuse.
func TestEnumerationInvariants(t *testing.T) {
	cfg := Config{
		Role:        "E",
		ReadSecret:  []byte("enum read secret"),
		WriteSecret: []byte("enum write secret"),
		Budget:      2,
		WindowSize:  4,
	}
	report, err := Enumerate(cfg, 6, 40000)
	if err != nil {
		t.Fatalf("enumeration error: %v", err)
	}
	if report.Violation != "" {
		t.Fatalf("invariant violation: %s\nhistory: %+v", report.Violation, report.ViolationLog)
	}
	if report.States < 100 {
		t.Fatalf("enumeration too small: %d states", report.States)
	}
	if !report.SeenSimul || !report.SeenCancel || !report.SeenDead || !report.SeenLate || !report.SeenCrossSeq || !report.SeenUnknown {
		t.Fatalf("coverage incomplete: %+v", report)
	}
	if report.MaxQueueLen < 2 {
		t.Fatalf("enumeration never built meaningful queues: %d", report.MaxQueueLen)
	}
	t.Logf("enumeration: states=%d transitions=%d traces=%d maxDepth=%d maxQueue=%d",
		report.States, report.Transitions, report.Traces, report.MaxDepth, report.MaxQueueLen)
}
