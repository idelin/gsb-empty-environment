package netsec

import (
	"testing"
	"time"
)

func TestMetadataConflictAndGenerationReuse(t *testing.T) {
	d := NewDetector()
	e := failAt(1, "10.0.0.1", 7000, "192.0.2.1", 22)
	d.Ingest(e)

	// Same (generation, instance), conflicting metadata is rejected.
	mut := e
	mut.Target.Port = 23
	res := d.Ingest(mut)
	if res.Status != IngestConflict || !res.Conflicting {
		t.Fatalf("mutated target = %+v, want conflict", res)
	}
	mut = e
	mut.Start = e.Start.Add(time.Second)
	if res = d.Ingest(mut); res.Status != IngestConflict {
		t.Fatalf("mutated start = %+v, want conflict", res)
	}
	mut = e
	mut.Source.Addr = "10.0.0.2"
	if res = d.Ingest(mut); res.Status != IngestConflict {
		t.Fatalf("mutated source = %+v, want conflict", res)
	}

	// The same instance number in a new generation is independent and may
	// legitimately reuse the number with different endpoints.
	other := ev(EventFailure, e.Generation+1, e.Instance, e.Start, "10.0.0.9", 7001, "192.0.2.9", 99)
	if res = d.Ingest(other); res.Status != IngestAccepted {
		t.Fatalf("cross-generation reuse = %+v, want accepted", res)
	}

	// Identical metadata repeated is a duplicate, not a conflict.
	if res = d.Ingest(e); res.Status != IngestDuplicate || res.Conflicting {
		t.Fatalf("identical repeat = %+v, want plain duplicate", res)
	}
}

func TestWatermarkLateAndFreeze(t *testing.T) {
	d := NewDetector()
	q := Query{Window: time.Minute, PortThreshold: 2, HostThreshold: 100, At: t0.Add(30 * time.Second)}
	id := mustRegister(t, d, q)

	f1 := failAt(5, "10.0.0.1", 8000, "192.0.2.1", 22)
	f2 := failAt(15, "10.0.0.1", 8001, "192.0.2.1", 23)
	d.Ingest(f1)
	d.Ingest(f2)
	if snap, _ := d.Snapshot(id); len(snap.Alerts) != 1 {
		t.Fatalf("alerts = %v, want 1", snap.Alerts)
	}

	// w = 10s: a correction at s < w is late, including success overrides.
	d.AdvanceWatermark(t0.Add(10 * time.Second))
	if res := d.Ingest(successOf(f1)); res.Status != IngestLate || !res.Late {
		t.Fatalf("late success = %+v, want late", res)
	}
	// A non-changing duplicate behind the watermark is still a duplicate.
	if res := d.Ingest(f1); res.Status != IngestDuplicate || !res.Late {
		t.Fatalf("late duplicate = %+v, want duplicate+late", res)
	}
	// Brand-new instances behind the watermark are rejected.
	lateNew := failAt(9, "10.0.0.1", 8002, "192.0.2.1", 24)
	if res := d.Ingest(lateNew); res.Status != IngestLate {
		t.Fatalf("late new = %+v, want late", res)
	}
	if snap, _ := d.Snapshot(id); len(snap.Alerts) != 1 {
		t.Fatalf("alerts = %v, late events must not change window", snap.Alerts)
	}

	// While w < t, an in-time correction withdraws the alert.
	if res := d.Ingest(successOf(f2)); res.Status != IngestDuplicate {
		t.Fatalf("in-time success = %+v", res)
	}
	snap, _ := d.Snapshot(id)
	if snap.Frozen || len(snap.Alerts) != 0 {
		t.Fatalf("snapshot = %+v, want open window with alert withdrawn", snap)
	}

	// w reaching t freezes the window; nothing can alter it afterwards.
	d.AdvanceWatermark(t0.Add(30 * time.Second))
	d.Ingest(f2) // would re-fail if allowed, but s < w
	snap, _ = d.Snapshot(id)
	if !snap.Frozen {
		t.Fatal("window should be frozen once w >= t")
	}
	if len(snap.Alerts) != 0 {
		t.Fatalf("frozen alerts = %v, want frozen empty result", snap.Alerts)
	}

	// Monotonic watermark ignores backwards moves.
	d.AdvanceWatermark(t0)
	if got := d.Watermark(); !got.Equal(t0.Add(30 * time.Second)) {
		t.Fatalf("watermark = %v, regressed", got)
	}

	// A query registered after the watermark passed t is frozen immediately.
	lateID, err := d.RegisterQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ := d.Snapshot(lateID); !snap.Frozen {
		t.Fatal("post-hoc query should be frozen at registration")
	}
}

func TestMemoryLimitMarksIncomplete(t *testing.T) {
	// Each event costs instanceOverhead + len(srcAddr) + len(dstAddr) = 273,
	// so two fit under three overheads but a third does not.
	d := NewDetector(WithMemoryLimit(3 * instanceOverhead))

	first := failAt(1, "10.0.0.1", 1100, "192.0.2.1", 22)
	second := failAt(2, "10.0.0.1", 1101, "192.0.2.1", 23)
	third := failAt(3, "10.0.0.1", 1102, "192.0.2.1", 24)
	if res := d.Ingest(first); res.Status != IngestAccepted {
		t.Fatalf("first = %v", res.Status)
	}
	if res := d.Ingest(second); res.Status != IngestAccepted {
		t.Fatalf("second = %v", res.Status)
	}
	if used := d.MemoryUsed(); used <= 0 {
		t.Fatalf("memory used = %d, want positive accounting", used)
	}
	res := d.Ingest(third)
	if res.Status != IngestMemoryLimit {
		t.Fatalf("third = %v, want memory limit", res.Status)
	}
	if !d.Incomplete() {
		t.Fatal("detector should be marked incomplete")
	}

	id := mustRegister(t, d, Query{Window: time.Minute, PortThreshold: 1, HostThreshold: 100, At: t0.Add(time.Minute)})
	snap, _ := d.Snapshot(id)
	if !snap.Incomplete {
		t.Fatal("snapshot must carry the incomplete flag")
	}

	// The flag is sticky even if pressure later subsides (limit never drops).
	if !d.Incomplete() {
		t.Fatal("incomplete must be sticky")
	}
}

func TestInvalidInputsAndQueryValidation(t *testing.T) {
	d := NewDetector()

	bad := Event{Kind: 99, Generation: 1, Instance: 1, Start: t0}
	if res := d.Ingest(bad); res.Status != IngestInvalid {
		t.Fatalf("bad kind = %v, want invalid", res.Status)
	}
	bad = Event{Kind: EventFailure, Generation: 1, Instance: 1, Source: Endpoint{Addr: "x"}, Target: Endpoint{Addr: "y"}}
	if res := d.Ingest(bad); res.Status != IngestInvalid {
		t.Fatalf("zero time = %v, want invalid", res.Status)
	}

	valid := Query{Window: time.Second, PortThreshold: 1, HostThreshold: 1, At: t0}
	for _, q := range []Query{
		{Window: 0, PortThreshold: 1, HostThreshold: 1, At: t0},
		{Window: time.Second, PortThreshold: 0, HostThreshold: 1, At: t0},
		{Window: time.Second, PortThreshold: 1, HostThreshold: 1, At: time.Time{}},
	} {
		if _, err := d.RegisterQuery(q); err == nil {
			t.Fatalf("query %+v should be rejected", q)
		}
	}
	if _, err := d.RegisterQuery(valid); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
	if _, ok := d.Snapshot(QueryID(9999)); ok {
		t.Fatal("unknown query should not snapshot")
	}
}

func TestRemoveQuery(t *testing.T) {
	d := NewDetector()
	id := mustRegister(t, d, Query{Window: time.Second, PortThreshold: 1, HostThreshold: 1, At: t0})
	if !d.RemoveQuery(id) {
		t.Fatal("query should exist")
	}
	if d.RemoveQuery(id) {
		t.Fatal("second removal should report false")
	}
}
