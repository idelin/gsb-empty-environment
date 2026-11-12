package recovmon_test

import (
	"testing"

	"example.org/netsec/q0423/recovmon"
)

func newTestMonitor(t *testing.T, raw recovmon.Interval, auth []recovmon.Interval) *recovmon.Monitor {
	t.Helper()
	mon, err := recovmon.NewMonitor(raw, auth)
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	return mon
}

// TestFirstViolationAcrossAuthenticatedBody builds a nested record with a
// partially truncated field and drives three recoveries through it. Only the
// second recovery reaches into authenticated bytes; it must become the first
// violation while later legal recoveries keep working.
func TestFirstViolationAcrossAuthenticatedBody(t *testing.T) {
	raw := recovmon.Interval{Start: 0, End: 128}
	authenticated := []recovmon.Interval{{Start: 64, End: 104}}
	mon := newTestMonitor(t, raw, authenticated)

	events := []recovmon.Event{
		recovmon.Begin("frame", 0, 128),
		recovmon.Begin("frame.header", 0, 24),
		recovmon.Consume(0, 16),
		recovmon.End("frame.header", recovmon.Interval{Start: 0, End: 24}),

		recovmon.Begin("frame.payload", 24, 104),
		recovmon.Begin("frame.payload.chunk1", 24, 48),
		recovmon.Consume(24, 36),

		// First corruption: chunk1 is partially truncated; recovery stays
		// unauthenticated and lands exactly on chunk2's start boundary.
		recovmon.Error(36, 40),
		recovmon.End("frame.payload.chunk1", recovmon.Interval{Start: 24, End: 36}),
		recovmon.Begin("frame.payload.chunk2", 48, 72),
		recovmon.Recovery(36, 48),

		recovmon.Consume(48, 56),
		recovmon.Error(56, 60),

		// chunk2 is truncated at the error point.
		recovmon.End("frame.payload.chunk2", recovmon.Interval{Start: 48, End: 56}),
		// mac begins exactly at the first authenticated byte; chunk3
		// continues inside the authenticated body.
		recovmon.Begin("frame.payload.mac", 64, 72),
		recovmon.End("frame.payload.mac", recovmon.Interval{Start: 64, End: 72}),
		recovmon.Begin("frame.payload.chunk3", 72, 104),
		// Illegal: mac is a real boundary but the discard crosses the
		// authenticated coverage starting at byte 64.
		recovmon.Recovery(56, 72),

		// Monitor must keep working: recover only to the boundary just
		// before the first authenticated byte, where mac starts.
		recovmon.Error(56, 60),
		recovmon.Recovery(56, 64),

		// Payload is partially truncated: it ends before its declared end.
		recovmon.End("frame.payload", recovmon.Interval{Start: 24, End: 64}),

		recovmon.Begin("frame.trailer", 104, 128),
		recovmon.Begin("frame.trailer.pad", 116, 124),
		recovmon.Error(108, 112),
		recovmon.Recovery(108, 116),
		recovmon.End("frame.trailer.pad", recovmon.Interval{Start: 116, End: 124}),
		recovmon.End("frame.trailer", recovmon.Interval{Start: 104, End: 128}),
		recovmon.End("frame", recovmon.Interval{Start: 0, End: 128}),
	}

	found := mon.ApplyBatch(events)
	if len(found) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %+v", len(found), found)
	}

	first := mon.FirstViolation()
	if first == nil {
		t.Fatal("FirstViolation returned nil")
	}
	if first.Index != 17 {
		t.Fatalf("first violation index = %d, want 17", first.Index)
	}
	if first.Kind != recovmon.ViolationAuthDiscard {
		t.Fatalf("kind = %q, want %q", first.Kind, recovmon.ViolationAuthDiscard)
	}
	if first.RecoveryCount != 2 {
		t.Fatalf("recovery count = %d, want 2", first.RecoveryCount)
	}
	if first.Discard != (recovmon.Interval{Start: 56, End: 72}) {
		t.Fatalf("discard = %v, want [56,72)", first.Discard)
	}
	if first.Target != 72 || first.FirstBadByte != 64 {
		t.Fatalf("target/firstBad = %d/%d, want 72/64", first.Target, first.FirstBadByte)
	}
	if len(first.AuthHits) != 1 || first.AuthHits[0] != (recovmon.Interval{Start: 64, End: 72}) {
		t.Fatalf("auth hits = %v, want [[64,72)]", first.AuthHits)
	}

	wantOpen := []string{"frame", "frame.payload", "frame.payload.chunk3"}
	if len(first.OpenFields) != len(wantOpen) {
		t.Fatalf("open field depth = %d, want %d", len(first.OpenFields), len(wantOpen))
	}
	for i, want := range wantOpen {
		if first.OpenFields[i].Path != want {
			t.Fatalf("open field %d = %q, want %q", i, first.OpenFields[i].Path, want)
		}
	}

	report := mon.Report()
	wantDiscard := []recovmon.Interval{
		{Start: 36, End: 48},
		{Start: 56, End: 64},
		{Start: 108, End: 116},
	}
	if len(report.Discarded) != len(wantDiscard) {
		t.Fatalf("discarded = %v, want %v", report.Discarded, wantDiscard)
	}
	for i := range wantDiscard {
		if report.Discarded[i] != wantDiscard[i] {
			t.Fatalf("discarded[%d] = %v, want %v", i, report.Discarded[i], wantDiscard[i])
		}
	}

	for _, d := range report.Discarded {
		for _, a := range report.Authenticated {
			if hit, ok := d.Intersects(a); ok {
				t.Fatalf("invariant broken: discard %v intersects auth %v at %v", d, a, hit)
			}
		}
	}

	sawTruncated := false
	for _, f := range report.Fields {
		if f.Path == "frame.payload.chunk2" && f.Truncated {
			if f.Consumed != (recovmon.Interval{Start: 48, End: 56}) {
				t.Fatalf("chunk2 consumed = %v, want [48,56)", f.Consumed)
			}
			sawTruncated = true
		}
	}
	if !sawTruncated {
		t.Fatalf("truncated payload missing from fields: %+v", report.Fields)
	}
}

func TestRecoveryRequiresUnconsumedFieldBoundary(t *testing.T) {
	mon := newTestMonitor(t, recovmon.Interval{Start: 0, End: 64}, nil)

	events := []recovmon.Event{
		recovmon.Begin("a", 0, 20),
		recovmon.Consume(0, 20),
		recovmon.End("a", recovmon.Interval{Start: 0, End: 20}),
		recovmon.Begin("b", 20, 40),
		recovmon.Error(20, 24),
		// Offset 30 is inside field b, not a field boundary.
		recovmon.Recovery(20, 30),
	}
	if v := mon.ApplyBatch(events); len(v) != 1 || v[0].Kind != recovmon.ViolationNotFieldBoundary {
		t.Fatalf("want single not-boundary violation, got %+v", v)
	}
	first := mon.FirstViolation()
	if len(first.CandidateBoundaries) == 0 || first.CandidateBoundaries[0] != 20 {
		t.Fatalf("candidate boundaries = %v, want first 20", first.CandidateBoundaries)
	}

	// Recovery without a pending error and non-forward recovery are rejected.
	mon2 := newTestMonitor(t, recovmon.Interval{Start: 0, End: 64}, nil)
	mon2.Apply(recovmon.Begin("x", 20, 30))
	if v := mon2.Apply(recovmon.Recovery(10, 20)); v == nil || v.Kind != recovmon.ViolationRecoverWithoutError {
		t.Fatalf("want recovery-without-error, got %+v", v)
	}
	mon2.Apply(recovmon.Error(10, 12))
	if v := mon2.Apply(recovmon.Recovery(20, 20)); v == nil || v.Kind != recovmon.ViolationInvalidSpan {
		t.Fatalf("want invalid-span for empty recovery, got %+v", v)
	}
}

func TestRecoveryCannotReprocessConsumedOrDiscarded(t *testing.T) {
	mon := newTestMonitor(t, recovmon.Interval{Start: 0, End: 96}, nil)

	mon.Apply(recovmon.Begin("a", 0, 32))
	mon.Apply(recovmon.Begin("b", 32, 64))
	mon.Apply(recovmon.Begin("c", 64, 96))
	mon.Apply(recovmon.Consume(0, 24))

	// Legal: skip to the boundary of b at 32.
	mon.Apply(recovmon.Error(24, 28))
	if v := mon.Apply(recovmon.Recovery(24, 32)); v != nil {
		t.Fatalf("first recovery should be legal: %+v", v)
	}

	// Re-discarding already discarded bytes is illegal.
	mon.Apply(recovmon.Error(24, 28))
	v := mon.Apply(recovmon.Recovery(24, 48))
	if v == nil || v.Kind != recovmon.ViolationReprocessed || v.FirstBadByte != 24 {
		t.Fatalf("want reprocessed violation at byte 24, got %+v", v)
	}

	// Discarding consumed bytes is illegal even when targeting a boundary.
	mon2 := newTestMonitor(t, recovmon.Interval{Start: 0, End: 96}, nil)
	mon2.Apply(recovmon.Begin("a", 0, 32))
	mon2.Apply(recovmon.Begin("b", 32, 64))
	mon2.Apply(recovmon.Consume(0, 32))
	mon2.Apply(recovmon.Error(20, 24))
	v = mon2.Apply(recovmon.Recovery(20, 32))
	if v == nil || v.Kind != recovmon.ViolationReprocessed || v.FirstBadByte != 20 {
		t.Fatalf("want consumed reprocess at byte 20, got %+v", v)
	}
}

func TestAuthenticatedByteCanNeverBeDiscarded(t *testing.T) {
	auth := []recovmon.Interval{{Start: 10, End: 12}}
	mon := newTestMonitor(t, recovmon.Interval{Start: 0, End: 32}, auth)

	mon.Apply(recovmon.Begin("f", 0, 32))
	mon.Apply(recovmon.Begin("g", 16, 24))
	mon.Apply(recovmon.Error(8, 10))
	v := mon.Apply(recovmon.Recovery(8, 16))
	if v == nil || v.Kind != recovmon.ViolationAuthDiscard {
		t.Fatalf("want authenticated-discard violation, got %+v", v)
	}
	if v.FirstBadByte != 10 || len(v.AuthHits) != 1 ||
		v.AuthHits[0] != (recovmon.Interval{Start: 10, End: 12}) {
		t.Fatalf("bad byte/hits = %d/%v", v.FirstBadByte, v.AuthHits)
	}
	if len(mon.Report().Discarded) != 0 {
		t.Fatal("rejected recovery must not record discarded bytes")
	}

	// Touching a single authenticated byte is enough.
	mon2 := newTestMonitor(t, recovmon.Interval{Start: 0, End: 32}, auth)
	mon2.Apply(recovmon.Begin("f", 0, 32))
	mon2.Apply(recovmon.Begin("g", 16, 24))
	mon2.Apply(recovmon.Error(11, 12))
	if v := mon2.Apply(recovmon.Recovery(11, 16)); v == nil {
		t.Fatal("single authenticated byte discard must be rejected")
	}
}

func TestNestedTruncationAndConsumeInvariants(t *testing.T) {
	mon := newTestMonitor(t, recovmon.Interval{Start: 0, End: 64},
		[]recovmon.Interval{{Start: 40, End: 64}})

	mon.Apply(recovmon.Begin("outer", 0, 64))
	mon.Apply(recovmon.Begin("outer.inner", 8, 40))

	// A child escaping its parent is a nesting violation but stays observable.
	v := mon.Apply(recovmon.Begin("outer.bad", 8, 50))
	if v == nil || v.Kind != recovmon.ViolationNesting {
		t.Fatalf("want nesting violation, got %+v", v)
	}

	mon.Apply(recovmon.Consume(8, 20))
	mon.Apply(recovmon.Error(20, 24))

	// A boundary inside authenticated coverage cannot be used: the discard
	// would cross byte 40, the first authenticated byte.
	mon.Apply(recovmon.Begin("outer.guard", 48, 56))
	if v := mon.Apply(recovmon.Recovery(20, 48)); v == nil ||
		v.Kind != recovmon.ViolationAuthDiscard {
		t.Fatalf("want authenticated discard, got %+v", v)
	}

	// Legal recovery to the start of a later unauthenticated field.
	mon.Apply(recovmon.Begin("outer.safe", 24, 32))
	if v := mon.Apply(recovmon.Recovery(20, 24)); v != nil {
		t.Fatalf("recovery to safe boundary should be legal: %+v", v)
	}

	// Reading discarded bytes afterwards is forbidden.
	if v := mon.Apply(recovmon.Consume(22, 28)); v == nil ||
		v.Kind != recovmon.ViolationConsumedDiscard {
		t.Fatalf("want consume-after-discard, got %+v", v)
	}

	// End inner early: partial truncation evidence.
	mon.Apply(recovmon.End("outer.inner", recovmon.Interval{Start: 8, End: 24}))
	report := mon.Report()
	var inner *recovmon.FieldInfo
	for i := range report.Fields {
		if report.Fields[i].Path == "outer.inner" {
			inner = &report.Fields[i]
		}
	}
	if inner == nil || !inner.Truncated || inner.Open {
		t.Fatalf("inner field should be truncated and closed: %+v", inner)
	}
	if inner.Consumed != (recovmon.Interval{Start: 8, End: 24}) {
		t.Fatalf("inner consumed = %v, want [8,24)", inner.Consumed)
	}

	if v := mon.Apply(recovmon.End("ghost", recovmon.Interval{Start: 0, End: 1})); v == nil ||
		v.Kind != recovmon.ViolationUnbalancedEnd {
		t.Fatalf("want unbalanced end, got %+v", v)
	}

	// Out-of-range and invalid spans are rejected.
	if v := mon.Apply(recovmon.Consume(60, 70)); v == nil ||
		v.Kind != recovmon.ViolationSpanOutOfRange {
		t.Fatalf("want out-of-range, got %+v", v)
	}
	if v := mon.Apply(recovmon.Consume(30, 30)); v == nil ||
		v.Kind != recovmon.ViolationInvalidSpan {
		t.Fatalf("want invalid span, got %+v", v)
	}
}

func TestNewMonitorRejectsBadConfiguration(t *testing.T) {
	if _, err := recovmon.NewMonitor(recovmon.Interval{Start: 10, End: 10}, nil); err == nil {
		t.Fatal("invalid raw interval must error")
	}
	raw := recovmon.Interval{Start: 0, End: 32}
	if _, err := recovmon.NewMonitor(raw, []recovmon.Interval{{Start: 20, End: 40}}); err == nil {
		t.Fatal("authenticated interval outside raw must error")
	}

	// Overlapping authenticated intervals are merged into one coverage set.
	mon, err := recovmon.NewMonitor(raw, []recovmon.Interval{{Start: 4, End: 12}, {Start: 10, End: 20}})
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	if got := mon.Report().Authenticated; len(got) != 1 || got[0] != (recovmon.Interval{Start: 4, End: 20}) {
		t.Fatalf("authenticated = %v, want single merged [4,20)", got)
	}
}
