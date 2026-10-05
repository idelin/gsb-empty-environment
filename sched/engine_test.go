package sched

import (
	"reflect"
	"testing"
)

func mustEngine(t *testing.T, positions []Position, shifts []Shift) *Engine {
	t.Helper()
	e, err := NewEngine(positions, shifts)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustAdd(t *testing.T, e *Engine, ev Event) []string {
	t.Helper()
	affected, err := e.AddEvent(ev)
	if err != nil {
		t.Fatalf("AddEvent %+v: %v", ev, err)
	}
	return affected
}

func mustCheck(t *testing.T, e *Engine, shiftID string, asOfSeq int) Report {
	t.Helper()
	rep, err := e.Check(shiftID, asOfSeq)
	if err != nil {
		t.Fatalf("Check(%q, %d): %v", shiftID, asOfSeq, err)
	}
	return rep
}

func issue(seq int, cert, emp, qual string, start, end Minutes) Event {
	return Event{Seq: seq, Kind: EventIssue, CertID: cert, Employee: emp, Qual: qual, Start: start, End: end}
}

func revoke(seq int, cert, emp, qual string, start, end Minutes) Event {
	return Event{Seq: seq, Kind: EventRevoke, CertID: cert, Employee: emp, Qual: qual, Start: start, End: end}
}

// Cross-midnight shift 22:00-06:00 ([1320, 1800)): a certificate expiring
// at midnight leaves the tail uncovered; a second certificate continuing
// past midnight closes the gap seamlessly.
func TestCrossMidnightShift(t *testing.T) {
	e := mustEngine(t,
		[]Position{{ID: "guard", RequiredQuals: []string{"license"}}},
		[]Shift{{ID: "night", Employee: "alice", Position: "guard", Start: 1320, End: 1800}},
	)
	mustAdd(t, e, issue(1, "c1", "alice", "license", 1200, 1440))

	rep := mustCheck(t, e, "night", 2)
	if rep.Schedulable {
		t.Fatal("expected gap after midnight")
	}
	want := []QualGap{{Qual: "license", Intervals: []Interval{{1440, 1800}}}}
	if !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", rep.Gaps, want)
	}

	affected := mustAdd(t, e, issue(2, "c2", "alice", "license", 1440, 1800))
	if !reflect.DeepEqual(affected, []string{"night"}) {
		t.Fatalf("affected = %v, want [night]", affected)
	}
	if rep := mustCheck(t, e, "night", 3); !rep.Schedulable {
		t.Fatalf("expected schedulable, got gaps %+v", rep.Gaps)
	}
}

// Two certificates chaining at an equal boundary ([0,720) + [720,1440))
// cover the whole shift: the shared endpoint is not a gap.
func TestSeamlessCertificateChaining(t *testing.T) {
	e := mustEngine(t,
		[]Position{{ID: "driver", RequiredQuals: []string{"permit"}}},
		[]Shift{{ID: "day", Employee: "bob", Position: "driver", Start: 0, End: 1440}},
	)
	mustAdd(t, e, issue(1, "c1", "bob", "permit", 0, 720))
	affected := mustAdd(t, e, issue(2, "c2", "bob", "permit", 720, 1440))
	if !reflect.DeepEqual(affected, []string{"day"}) {
		t.Fatalf("affected = %v, want [day]", affected)
	}
	if rep := mustCheck(t, e, "day", 3); !rep.Schedulable {
		t.Fatalf("chained certificates must be seamless, got %+v", rep.Gaps)
	}
}

// Overlapping revocations union into one minimal gap, and a revocation
// only affects queries recorded after it.
func TestOverlappingRevocations(t *testing.T) {
	e := mustEngine(t,
		[]Position{{ID: "guard", RequiredQuals: []string{"license"}}},
		[]Shift{{ID: "day", Employee: "carol", Position: "guard", Start: 0, End: 1440}},
	)
	mustAdd(t, e, issue(1, "c1", "carol", "license", 0, 1440))
	mustAdd(t, e, revoke(2, "c1", "carol", "license", 100, 200))
	mustAdd(t, e, revoke(3, "c1", "carol", "license", 150, 300))

	if rep := mustCheck(t, e, "day", 2); !rep.Schedulable {
		t.Fatalf("revocation not yet known at seq 2, got %+v", rep.Gaps)
	}
	rep := mustCheck(t, e, "day", 3)
	want := []QualGap{{Qual: "license", Intervals: []Interval{{100, 200}}}}
	if !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("gaps at seq 3 = %+v, want %+v", rep.Gaps, want)
	}
	rep = mustCheck(t, e, "day", 4)
	want = []QualGap{{Qual: "license", Intervals: []Interval{{100, 300}}}}
	if !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("overlapping revocations must union: gaps = %+v, want %+v", rep.Gaps, want)
	}
}

// A retroactively recorded certificate fixes new queries but must not
// change the answer of a query issued before it was known.
func TestRetroactiveCertificateKeepsOldQueries(t *testing.T) {
	e := mustEngine(t,
		[]Position{{ID: "medic", RequiredQuals: []string{"cpr"}}},
		[]Shift{{ID: "s1", Employee: "dan", Position: "medic", Start: 100, End: 200}},
	)
	before := mustCheck(t, e, "s1", 1)
	if before.Schedulable {
		t.Fatal("expected uncovered shift before any certificate")
	}

	affected := mustAdd(t, e, issue(5, "c1", "dan", "cpr", 100, 200))
	if !reflect.DeepEqual(affected, []string{"s1"}) {
		t.Fatalf("affected = %v, want [s1]", affected)
	}

	again := mustCheck(t, e, "s1", 1)
	if !reflect.DeepEqual(again, before) {
		t.Fatalf("old query changed: before %+v, after %+v", before, again)
	}
	if rep := mustCheck(t, e, "s1", 5); !reflect.DeepEqual(rep, before) {
		t.Fatalf("query as of seq 5 must not see seq-5 event: %+v", rep)
	}
	if rep := mustCheck(t, e, "s1", 6); !rep.Schedulable {
		t.Fatalf("query as of seq 6 must see the certificate, got %+v", rep.Gaps)
	}
}

// One employee holding two positions: each shift is checked against its
// own position's required qualifications.
func TestEmployeeWithMultiplePositions(t *testing.T) {
	e := mustEngine(t,
		[]Position{
			{ID: "driver", RequiredQuals: []string{"permit"}},
			{ID: "medic", RequiredQuals: []string{"cpr", "firstaid"}},
		},
		[]Shift{
			{ID: "drive", Employee: "erin", Position: "driver", Start: 480, End: 960},
			{ID: "med", Employee: "erin", Position: "medic", Start: 480, End: 960},
		},
	)
	mustAdd(t, e, issue(1, "c1", "erin", "permit", 0, 1440))
	mustAdd(t, e, issue(2, "c2", "erin", "cpr", 0, 1440))

	if rep := mustCheck(t, e, "drive", 3); !rep.Schedulable {
		t.Fatalf("drive shift should be schedulable, got %+v", rep.Gaps)
	}
	rep := mustCheck(t, e, "med", 3)
	want := []QualGap{{Qual: "firstaid", Intervals: []Interval{{480, 960}}}}
	if rep.Schedulable || !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("med shift gaps = %+v, want %+v", rep.Gaps, want)
	}
}

// Certificate expiring mid-shift produces a gap for the tail only.
func TestCertificateExpiresMidShift(t *testing.T) {
	e := mustEngine(t,
		[]Position{{ID: "guard", RequiredQuals: []string{"license"}}},
		[]Shift{{ID: "s1", Employee: "fred", Position: "guard", Start: 600, End: 900}},
	)
	mustAdd(t, e, issue(1, "c1", "fred", "license", 0, 750))
	rep := mustCheck(t, e, "s1", 2)
	want := []QualGap{{Qual: "license", Intervals: []Interval{{750, 900}}}}
	if !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", rep.Gaps, want)
	}
}

// The index-based incremental recheck must agree with the full-scan
// reference for every event, and events touching no candidate must not
// affect any shift.
func TestIndexMatchesFullScan(t *testing.T) {
	positions := []Position{
		{ID: "driver", RequiredQuals: []string{"permit"}},
		{ID: "medic", RequiredQuals: []string{"cpr", "firstaid"}},
	}
	shifts := []Shift{
		{ID: "a-night", Employee: "amy", Position: "driver", Start: 1320, End: 1800},
		{ID: "a-day", Employee: "amy", Position: "medic", Start: 300, End: 700},
		{ID: "b-day", Employee: "ben", Position: "driver", Start: 0, End: 1440},
		{ID: "c-short", Employee: "cy", Position: "medic", Start: 100, End: 160},
	}
	events := []Event{
		issue(1, "c1", "amy", "permit", 0, 1440),
		issue(2, "c2", "amy", "permit", 1440, 2000),
		issue(3, "c3", "amy", "cpr", 0, 500),
		issue(4, "c4", "amy", "firstaid", 400, 800),
		revoke(5, "c1", "amy", "permit", 600, 700),
		revoke(6, "c1", "amy", "permit", 650, 900),
		issue(7, "c5", "ben", "permit", 0, 1440),
		revoke(8, "c5", "ben", "permit", 2000, 2100), // outside every shift
		issue(9, "c6", "cy", "cpr", 100, 160),
		issue(10, "c7", "cy", "firstaid", 100, 160),
		revoke(11, "c6", "cy", "cpr", 120, 140),
		issue(12, "c8", "amy", "cpr", 500, 700), // retroactive chaining fix
	}

	indexed := mustEngine(t, positions, shifts)
	scanned := mustEngine(t, positions, shifts)
	for _, ev := range events {
		got, err := indexed.AddEvent(ev)
		if err != nil {
			t.Fatalf("AddEvent %+v: %v", ev, err)
		}
		want, err := scanned.AffectedByFullScan(ev)
		if err != nil {
			t.Fatalf("AffectedByFullScan %+v: %v", ev, err)
		}
		if err := scanned.store.Add(ev); err != nil {
			t.Fatalf("store.Add %+v: %v", ev, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("event seq %d: index affected %v, full scan %v", ev.Seq, got, want)
		}
	}

	// Spot-check final state: amy night shift fully covered by chained
	// permits, day shift missing cpr in [500,700) until seq 12 is known.
	if rep := mustCheck(t, indexed, "a-night", 13); !rep.Schedulable {
		t.Fatalf("a-night should be covered, got %+v", rep.Gaps)
	}
	rep := mustCheck(t, indexed, "a-day", 12)
	want := []QualGap{
		{Qual: "cpr", Intervals: []Interval{{500, 700}}},
		{Qual: "firstaid", Intervals: []Interval{{300, 400}}},
	}
	if !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("a-day gaps at seq 12 = %+v, want %+v", rep.Gaps, want)
	}
	if rep := mustCheck(t, indexed, "a-day", 13); rep.Schedulable {
		t.Fatalf("a-day still misses firstaid [300,400), got %+v", rep.Gaps)
	}
	// c-short: revocation [120,140) of cpr known from seq 12 on.
	rep = mustCheck(t, indexed, "c-short", 13)
	want = []QualGap{{Qual: "cpr", Intervals: []Interval{{120, 140}}}}
	if !reflect.DeepEqual(rep.Gaps, want) {
		t.Fatalf("c-short gaps = %+v, want %+v", rep.Gaps, want)
	}
}

func TestAggregateByQual(t *testing.T) {
	reps := []Report{
		{ShiftID: "s1", Schedulable: false, Gaps: []QualGap{
			{Qual: "cpr", Intervals: []Interval{{10, 20}}},
			{Qual: "firstaid", Intervals: []Interval{{10, 30}}},
		}},
		{ShiftID: "s2", Schedulable: false, Gaps: []QualGap{
			{Qual: "cpr", Intervals: []Interval{{50, 60}}},
		}},
		{ShiftID: "s3", Schedulable: true},
	}
	got := AggregateByQual(reps)
	want := []QualImpact{
		{Qual: "cpr", Shifts: []ShiftGaps{
			{ShiftID: "s1", Intervals: []Interval{{10, 20}}},
			{ShiftID: "s2", Intervals: []Interval{{50, 60}}},
		}},
		{Qual: "firstaid", Shifts: []ShiftGaps{
			{ShiftID: "s1", Intervals: []Interval{{10, 30}}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AggregateByQual = %+v, want %+v", got, want)
	}
}

func TestMergeTouchingIntervals(t *testing.T) {
	got := Merge([]Interval{{720, 1440}, {0, 720}, {2000, 2100}})
	want := []Interval{{0, 1440}, {2000, 2100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewEngine(nil, []Shift{{ID: "x", Employee: "e", Position: "ghost", Start: 0, End: 10}}); err == nil {
		t.Fatal("expected error for unknown position")
	}
	e := mustEngine(t,
		[]Position{{ID: "p", RequiredQuals: []string{"q"}}},
		[]Shift{{ID: "s", Employee: "e", Position: "p", Start: 0, End: 10}},
	)
	if _, err := e.AddEvent(Event{Seq: 1, Kind: "bogus", CertID: "c", Employee: "e", Qual: "q", Start: 0, End: 10}); err == nil {
		t.Fatal("expected error for unknown event kind")
	}
	if _, err := e.AddEvent(issue(1, "c", "e", "q", 10, 10)); err == nil {
		t.Fatal("expected error for empty validity interval")
	}
	mustAdd(t, e, issue(1, "c", "e", "q", 0, 10))
	if _, err := e.AddEvent(issue(1, "c2", "e", "q", 0, 10)); err == nil {
		t.Fatal("expected error for non-increasing sequence")
	}
	if _, err := e.Check("missing", 1); err == nil {
		t.Fatal("expected error for unknown shift")
	}
}
