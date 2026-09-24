package cacheaudit

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func pub(seq uint64, key string, version uint64, sec int) Event {
	return Event{Seq: seq, Kind: KindPublish, Key: key, Version: version, At: at(sec)}
}

func call(seq uint64, key string, version uint64, start, end int, oc Outcome) Event {
	return Event{Seq: seq, Kind: KindCall, Key: key, Version: version, Start: at(start), End: at(end), Outcome: oc}
}

func TestConsistentHistory(t *testing.T) {
	h := History{Events: []Event{
		pub(1, "a", 1, 1),
		call(2, "a", 1, 2, 3, OutcomeHit),
		pub(3, "a", 2, 4),
		call(4, "a", 2, 5, 6, OutcomeHit),
	}}
	if rep := Audit(h); rep.Verdict != Consistent {
		t.Fatalf("audit = %v, want CONSISTENT: %+v", rep.Verdict, rep)
	}
}

func TestConcurrentCallMayLinearizeBeforePublish(t *testing.T) {
	h := History{Events: []Event{
		pub(1, "a", 1, 1),
		call(2, "a", 1, 2, 5, OutcomeHit), // overlaps the v2 publish
		pub(3, "a", 2, 3),
	}}
	if rep := Audit(h); rep.Verdict != Consistent {
		t.Fatalf("audit = %v, want CONSISTENT (call can linearize before publish)", rep.Verdict)
	}
}

func TestStaleReadDetectedAndLocated(t *testing.T) {
	h := History{Events: []Event{
		pub(1, "a", 1, 1),
		pub(2, "a", 2, 2),
		call(3, "a", 1, 3, 4, OutcomeHit), // v2 published before the call began
	}}
	rep := Audit(h)
	if rep.Verdict != Stale {
		t.Fatalf("audit = %v, want STALE", rep.Verdict)
	}
	if len(rep.Violations) != 1 {
		t.Fatalf("violations = %+v", rep.Violations)
	}
	v := rep.Violations[0]
	if v.ReturnedVersion != 1 || v.ExpectedVersion != 2 {
		t.Fatalf("violation = %+v", v)
	}
	if v.SourcePublish == nil || v.SourcePublish.Seq != 1 {
		t.Fatalf("source-version evidence missing: %+v", v)
	}
	if v.ExpectedPublish == nil || v.ExpectedPublish.Seq != 2 {
		t.Fatalf("expected publish evidence missing: %+v", v)
	}
	if !strings.Contains(v.Detail, "v1") || !strings.Contains(v.Detail, "v2") {
		t.Fatalf("detail does not locate the cause: %s", v.Detail)
	}
	// Minimization must keep the source-version evidence.
	if len(rep.Minimal) == 0 {
		t.Fatalf("no minimal subset produced")
	}
	if !sourcesTraceable(rep.Minimal) {
		t.Fatalf("minimal subset lost source-version evidence: %v", rep.Minimal)
	}
	if got := auditCore(History{Events: rep.Minimal}).Verdict; got != Stale {
		t.Fatalf("minimal subset verdict = %v, want STALE", got)
	}
}

func TestDroppedLogIsInconclusiveNotStale(t *testing.T) {
	// Same shape as the stale history, but the log lost events: the auditor
	// must refuse to decide rather than guess the full history.
	h := History{
		Events: []Event{
			pub(1, "a", 1, 1),
			call(3, "a", 1, 3, 4, OutcomeHit),
		},
		Meta: LogMeta{Dropped: 5, Capacity: 2, Count: 2, SeqVerified: true},
	}
	rep := Audit(h)
	if rep.Verdict != Inconclusive {
		t.Fatalf("audit = %v, want INCONCLUSIVE", rep.Verdict)
	}
	if len(rep.Incomplete) == 0 || !strings.Contains(rep.Incomplete[0], "lost") {
		t.Fatalf("missing evidence-gap explanation: %+v", rep.Incomplete)
	}
}

func TestSequenceGapIsInconclusive(t *testing.T) {
	h := History{
		Events: []Event{
			pub(1, "a", 1, 1),
			call(4, "a", 1, 2, 3, OutcomeHit), // seq 2..3 lost
		},
		Meta: LogMeta{Capacity: 8, Count: 2, FirstSeq: 1, LastSeq: 4, SeqVerified: true},
	}
	if rep := Audit(h); rep.Verdict != Inconclusive {
		t.Fatalf("audit = %v, want INCONCLUSIVE on sequence gap", rep.Verdict)
	}
}

func TestUnpublishedReturnIsViolation(t *testing.T) {
	h := History{Events: []Event{
		pub(1, "a", 1, 1),
		call(2, "a", 9, 2, 3, OutcomeHit), // v9 never published; log complete
	}}
	rep := Audit(h)
	if rep.Verdict != Stale {
		t.Fatalf("audit = %v, want STALE for unpublished return", rep.Verdict)
	}
	if !strings.Contains(rep.Violations[0].Detail, "never published") {
		t.Fatalf("detail = %s", rep.Violations[0].Detail)
	}
}

func TestErrorCallsAreNotReads(t *testing.T) {
	h := History{Events: []Event{
		call(1, "a", 0, 1, 2, OutcomeError),
	}}
	if rep := Audit(h); rep.Verdict != Consistent {
		t.Fatalf("audit = %v, want CONSISTENT (error calls return nothing)", rep.Verdict)
	}
}

func TestKeysAuditedIndependently(t *testing.T) {
	h := History{Events: []Event{
		pub(1, "a", 1, 1),
		pub(2, "a", 2, 2),
		call(3, "a", 1, 3, 4, OutcomeHit), // stale on a
		pub(4, "b", 1, 1),
		call(5, "b", 1, 2, 3, OutcomeHit), // fine on b
	}}
	rep := Audit(h)
	if rep.Verdict != Stale {
		t.Fatalf("audit = %v, want STALE", rep.Verdict)
	}
	if len(rep.Violations) != 1 || rep.Violations[0].Key != "a" {
		t.Fatalf("violations = %+v", rep.Violations)
	}
}
