package netsec

import (
	"testing"
	"time"
)

var t0 = time.Unix(1_000_000, 0)

func ev(kind EventKind, gen, inst uint64, s time.Time, srcAddr string, srcPort uint16, dstAddr string, dstPort uint16) Event {
	return Event{
		Kind:       kind,
		Generation: gen,
		Instance:   inst,
		Start:      s,
		Source:     Endpoint{Addr: srcAddr, Port: srcPort},
		Target:     Endpoint{Addr: dstAddr, Port: dstPort},
	}
}

func failAt(sec int64, srcAddr string, srcPort uint16, dstAddr string, dstPort uint16) Event {
	return ev(EventFailure, 1, uint64(srcPort)*100+uint64(dstPort), t0.Add(time.Duration(sec)*time.Second),
		srcAddr, srcPort, dstAddr, dstPort)
}

func mustRegister(t *testing.T, d *Detector, q Query) QueryID {
	t.Helper()
	id, err := d.RegisterQuery(q)
	if err != nil {
		t.Fatalf("RegisterQuery: %v", err)
	}
	return id
}

func TestWindowBoundaries(t *testing.T) {
	d := NewDetector()
	q := Query{Window: 10 * time.Second, PortThreshold: 1, HostThreshold: 100, At: t0.Add(10 * time.Second)}
	id := mustRegister(t, d, q)

	// s == t-W is inside [t-W, t); s == t is excluded.
	d.Ingest(failAt(0, "10.0.0.1", 1000, "192.0.2.1", 22))
	d.Ingest(failAt(5, "10.0.0.1", 1001, "192.0.2.1", 23))
	d.Ingest(failAt(10, "10.0.0.1", 1002, "192.0.2.1", 24))

	snap, ok := d.Snapshot(id)
	if !ok {
		t.Fatal("snapshot missing")
	}
	if got := len(snap.Alerts); got != 1 {
		t.Fatalf("alerts = %d, want 1 (ports 22 and 23 in window)", got)
	}
	if snap.Alerts[0].Count != 2 {
		t.Fatalf("count = %d, want 2", snap.Alerts[0].Count)
	}
}

func TestRetransmissionsDoNotInflateCounts(t *testing.T) {
	d := NewDetector()
	q := Query{Window: time.Minute, PortThreshold: 3, HostThreshold: 100, At: t0.Add(time.Minute)}
	id := mustRegister(t, d, q)

	e := failAt(1, "10.0.0.1", 4000, "192.0.2.1", 22)
	for i := 0; i < 5; i++ {
		res := d.Ingest(e)
		if i == 0 && res.Status != IngestAccepted {
			t.Fatalf("first ingest = %v, want accepted", res.Status)
		}
		if i > 0 && res.Status != IngestDuplicate {
			t.Fatalf("retry %d = %v, want duplicate", i, res.Status)
		}
	}
	// A retransmission from a new ephemeral source port against the same
	// host/port tuple still resolves to one distinct failed destination port.
	d.Ingest(failAt(2, "10.0.0.1", 4001, "192.0.2.1", 22))

	snap, _ := d.Snapshot(id)
	if len(snap.Alerts) != 0 {
		t.Fatalf("alerts = %v, want none below threshold", snap.Alerts)
	}

	d.Ingest(failAt(3, "10.0.0.1", 4002, "192.0.2.1", 23))
	d.Ingest(failAt(4, "10.0.0.1", 4003, "192.0.2.1", 24))
	snap, _ = d.Snapshot(id)
	if len(snap.Alerts) != 1 || snap.Alerts[0].Count != 3 {
		t.Fatalf("alerts = %v, want one host sweep over 3 ports", snap.Alerts)
	}
}

func successOf(e Event) Event {
	cp := e
	cp.Kind = EventSuccess
	return cp
}

func TestSuccessOverridesFailureAndIsTerminal(t *testing.T) {
	d := NewDetector()
	q := Query{Window: time.Minute, PortThreshold: 2, HostThreshold: 100, At: t0.Add(time.Minute)}
	id := mustRegister(t, d, q)

	f1 := failAt(1, "10.0.0.1", 5000, "192.0.2.1", 22)
	f2 := failAt(2, "10.0.0.1", 5001, "192.0.2.1", 23)
	d.Ingest(f1)
	d.Ingest(f2)
	if snap, _ := d.Snapshot(id); len(snap.Alerts) != 1 {
		t.Fatalf("alerts = %v, want 1 before success", snap.Alerts)
	}

	// Out-of-order success for instance 1 overrides its failure.
	if res := d.Ingest(successOf(f1)); res.Status != IngestDuplicate {
		t.Fatalf("success override = %v, want duplicate status", res.Status)
	}
	snap, _ := d.Snapshot(id)
	if len(snap.Alerts) != 0 {
		t.Fatalf("alerts = %v, want withdrawn after success", snap.Alerts)
	}

	// A retransmitted failure after success must not resurrect the count.
	if res := d.Ingest(f1); res.Status != IngestDuplicate {
		t.Fatalf("late failure = %v, want duplicate", res.Status)
	}
	snap, _ = d.Snapshot(id)
	if len(snap.Alerts) != 0 {
		t.Fatalf("alerts = %v, success must stay terminal", snap.Alerts)
	}

	// Success observed before any failure also keeps the instance clean.
	f3 := failAt(3, "10.0.0.1", 5002, "192.0.2.1", 24)
	d.Ingest(successOf(f3))
	d.Ingest(f3)
	snap, _ = d.Snapshot(id)
	if len(snap.Alerts) != 0 {
		t.Fatalf("alerts = %v, success-first instance must never count", snap.Alerts)
	}
}

func TestPortTargetSweepThreshold(t *testing.T) {
	d := NewDetector()
	q := Query{Window: time.Minute, PortThreshold: 100, HostThreshold: 3, At: t0.Add(time.Minute)}
	id := mustRegister(t, d, q)

	for i, host := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		d.Ingest(failAt(int64(i), "10.0.0.1", uint16(6000+i), host, 443))
	}
	snap, _ := d.Snapshot(id)
	if len(snap.Alerts) != 1 {
		t.Fatalf("alerts = %v, want 1", snap.Alerts)
	}
	a := snap.Alerts[0]
	if a.Kind != AlertPortTargetSweep || a.Port != 443 || a.Count != 3 {
		t.Fatalf("alert = %+v, want port-target sweep on 443 over 3 hosts", a)
	}
}
