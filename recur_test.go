package recur

import (
	"math"
	"testing"
	"time"
)

func mustRule(t *testing.T, id, start string, weeks int) *Rule {
	t.Helper()
	d, err := time.Parse("2006-01-02", start)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRule(id, d, weeks)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustRev(t *testing.T, r *Rule, fromSeq, rev int, wd time.Weekday, minute, dur, off int) {
	t.Helper()
	if err := r.UpsertRevision(Revision{FromSeq: fromSeq, Rev: rev,
		Params: Params{Weekday: wd, Minute: minute, DurationMin: dur, OffsetMin: off}}); err != nil {
		t.Fatal(err)
	}
}

func unix(t *testing.T, s string) int64 {
	t.Helper()
	v, err := ParseTime(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ids(insts []Instance) []string {
	out := make([]string, len(insts))
	for i, inst := range insts {
		out[i] = inst.ID
	}
	return out
}

// Moving a Monday instance to Tuesday must make Monday queries miss and
// Tuesday queries hit, with identities unchanged.
func TestMondayMovedToTuesday(t *testing.T) {
	r := mustRule(t, "standup", "2026-01-05", 4)
	mustRev(t, r, 0, 1, time.Monday, 9*60, 30, 0)
	mustRev(t, r, 0, 2, time.Tuesday, 9*60, 30, 0)

	mon := r.QueryReference(unix(t, "2026-01-05T08:00:00Z"), unix(t, "2026-01-05T10:00:00Z"))
	if len(mon) != 0 {
		t.Fatalf("Monday window should be empty, got %v", ids(mon))
	}
	tue := r.QueryReference(unix(t, "2026-01-06T08:00:00Z"), unix(t, "2026-01-06T10:00:00Z"))
	if len(tue) != 1 || tue[0].ID != "standup#0" {
		t.Fatalf("Tuesday window should hold standup#0, got %v", ids(tue))
	}
	if tue[0].Start != unix(t, "2026-01-06T09:00:00Z") || tue[0].End != unix(t, "2026-01-06T09:30:00Z") {
		t.Fatalf("unexpected times %v..%v", FormatTime(tue[0].Start), FormatTime(tue[0].End))
	}
	if tue[0].Rev != 2 {
		t.Fatalf("adopted rev = %d, want 2", tue[0].Rev)
	}
}

// A 23:50 + 40min instance crosses midnight and must be found by a query
// covering only the early hours of the next day, and found exactly once by a
// query spanning both days.
func TestCrossMidnight(t *testing.T) {
	r := mustRule(t, "late", "2026-01-05", 2)
	mustRev(t, r, 0, 1, time.Monday, 23*60+50, 40, 0)

	nextDay := r.QueryReference(unix(t, "2026-01-06T00:00:00Z"), unix(t, "2026-01-06T01:00:00Z"))
	if len(nextDay) != 1 || nextDay[0].ID != "late#0" {
		t.Fatalf("next-day window should hold late#0, got %v", ids(nextDay))
	}
	if nextDay[0].End != unix(t, "2026-01-06T00:30:00Z") {
		t.Fatalf("end = %v, want 2026-01-06T00:30:00Z", FormatTime(nextDay[0].End))
	}
	span := r.QueryReference(unix(t, "2026-01-05T00:00:00Z"), unix(t, "2026-01-07T00:00:00Z"))
	if len(span) != 1 {
		t.Fatalf("spanning query should find the instance once, got %d", len(span))
	}
	// A window ending exactly at the start must not match (half-open).
	touch := r.QueryReference(unix(t, "2026-01-05T22:00:00Z"), unix(t, "2026-01-05T23:50:00Z"))
	if len(touch) != 0 {
		t.Fatalf("half-open boundary should exclude instance, got %v", ids(touch))
	}
}

// Changing the fixed UTC offset shifts UTC times but never identities, and
// week indices stay anchored to the original start date.
func TestOffsetChangeKeepsIdentity(t *testing.T) {
	r := mustRule(t, "sync", "2026-01-05", 4)
	mustRev(t, r, 0, 1, time.Monday, 9*60, 30, 0)
	mustRev(t, r, 2, 2, time.Monday, 9*60, 30, 8*60) // +08:00 from week 2

	inst2, ok := r.Instance(2)
	if !ok {
		t.Fatal("seq 2 should exist")
	}
	if inst2.ID != "sync#2" {
		t.Fatalf("identity drifted: %q", inst2.ID)
	}
	if inst2.Start != unix(t, "2026-01-19T01:00:00Z") { // 09:00 at +08:00
		t.Fatalf("start = %v, want 2026-01-19T01:00:00Z", FormatTime(inst2.Start))
	}
	inst1, _ := r.Instance(1)
	if inst1.Start != unix(t, "2026-01-12T09:00:00Z") {
		t.Fatalf("seq 1 start = %v, want 2026-01-12T09:00:00Z", FormatTime(inst1.Start))
	}
	// Week index of a date must not depend on the offset in effect.
	seq, ok := r.SeqForDate(time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC))
	if !ok || seq != 2 {
		t.Fatalf("SeqForDate = %d,%v want 2,true", seq, ok)
	}
}

// A late-night instance in a non-UTC offset lands on a different UTC date;
// its identity must still be the week of its local date.
func TestOffsetAcrossUTCDateBoundary(t *testing.T) {
	r := mustRule(t, "owl", "2026-01-05", 2)
	mustRev(t, r, 0, 1, time.Tuesday, 30, 60, 8*60) // Tue 00:30 +08:00 = Mon 16:30 UTC
	inst, ok := r.Instance(0)
	if !ok {
		t.Fatal("seq 0 should exist")
	}
	if inst.ID != "owl#0" || inst.Start != unix(t, "2026-01-05T16:30:00Z") {
		t.Fatalf("got %v at %v", inst.ID, FormatTime(inst.Start))
	}
}

// An override that moves an occurrence outside the original query window must
// remove it there and surface it at the new time.
func TestOverrideMovesOutsideBoundary(t *testing.T) {
	r := mustRule(t, "plan", "2026-01-05", 4)
	mustRev(t, r, 0, 1, time.Monday, 9*60, 30, 0)
	if err := r.UpsertException(Exception{Seq: 1, Rev: 1, Action: ActionOverride,
		Params: Params{Weekday: time.Friday, Minute: 17 * 60, DurationMin: 30, OffsetMin: 0}}); err != nil {
		t.Fatal(err)
	}
	mon := r.QueryReference(unix(t, "2026-01-12T00:00:00Z"), unix(t, "2026-01-13T00:00:00Z"))
	if len(mon) != 0 {
		t.Fatalf("Monday of week 1 should be empty, got %v", ids(mon))
	}
	fri := r.QueryReference(unix(t, "2026-01-16T16:00:00Z"), unix(t, "2026-01-16T18:00:00Z"))
	if len(fri) != 1 || fri[0].ID != "plan#1" || fri[0].Source != SourceOverride || fri[0].Rev != 1 {
		t.Fatalf("Friday should hold overridden plan#1, got %+v", fri)
	}
}

// Cancel removes an occurrence, restore brings back the range-rule version,
// and at equal revision numbers cancel beats override.
func TestCancelRestore(t *testing.T) {
	r := mustRule(t, "retro", "2026-01-05", 3)
	mustRev(t, r, 0, 1, time.Wednesday, 14*60, 60, 0)

	if err := r.UpsertException(Exception{Seq: 1, Rev: 1, Action: ActionCancel}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Instance(1); ok {
		t.Fatal("seq 1 should be canceled")
	}
	if err := r.UpsertException(Exception{Seq: 1, Rev: 2, Action: ActionRestore}); err != nil {
		t.Fatal(err)
	}
	inst, ok := r.Instance(1)
	if !ok {
		t.Fatal("seq 1 should be restored")
	}
	if inst.Start != unix(t, "2026-01-14T14:00:00Z") || inst.Source != SourceRange {
		t.Fatalf("restored instance = %v (%s)", FormatTime(inst.Start), inst.Source)
	}

	// Same-revision tie: cancel wins over override.
	if err := r.UpsertException(Exception{Seq: 2, Rev: 1, Action: ActionOverride,
		Params: Params{Weekday: time.Thursday, Minute: 10 * 60, DurationMin: 60, OffsetMin: 0}}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertException(Exception{Seq: 2, Rev: 1, Action: ActionCancel}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Instance(2); ok {
		t.Fatal("cancel should beat override at equal rev")
	}
	// Higher-rev override beats an older cancel.
	if err := r.UpsertException(Exception{Seq: 2, Rev: 2, Action: ActionOverride,
		Params: Params{Weekday: time.Thursday, Minute: 10 * 60, DurationMin: 60, OffsetMin: 0}}); err != nil {
		t.Fatal(err)
	}
	inst2, ok := r.Instance(2)
	if !ok || inst2.Start != unix(t, "2026-01-22T10:00:00Z") {
		t.Fatalf("newer override should win, got %+v", inst2)
	}
}

// Instances beyond the finite horizon must not exist.
func TestFiniteHorizon(t *testing.T) {
	r := mustRule(t, "fin", "2026-01-05", 2)
	mustRev(t, r, 0, 1, time.Monday, 9*60, 30, 0)
	if _, ok := r.Instance(2); ok {
		t.Fatal("seq 2 is beyond weeks=2")
	}
	if got := len(r.AllInstances()); got != 2 {
		t.Fatalf("AllInstances = %d, want 2", got)
	}
}

func TestValidationAndOverflow(t *testing.T) {
	if _, err := NewRule("x", time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), 4); err == nil {
		t.Fatal("non-Monday start should fail")
	}
	if _, err := NewRule("x", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), 0); err == nil {
		t.Fatal("weeks=0 should fail")
	}
	r := mustRule(t, "v", "2026-01-05", 4)
	bad := []Params{
		{Weekday: time.Monday, Minute: 1440, DurationMin: 30, OffsetMin: 0},
		{Weekday: time.Monday, Minute: -1, DurationMin: 30, OffsetMin: 0},
		{Weekday: time.Monday, Minute: 0, DurationMin: 0, OffsetMin: 0},
		{Weekday: time.Monday, Minute: 0, DurationMin: MaxDurationMin + 1, OffsetMin: 0},
		{Weekday: time.Monday, Minute: 0, DurationMin: 30, OffsetMin: MaxOffsetMin + 1},
		{Weekday: time.Monday, Minute: 0, DurationMin: 30, OffsetMin: -MaxOffsetMin - 1},
	}
	for i, p := range bad {
		if err := r.UpsertRevision(Revision{FromSeq: 0, Rev: i + 1, Params: p}); err == nil {
			t.Fatalf("bad params %d accepted", i)
		}
	}
	if _, err := minutesToSeconds(math.MaxInt64); err != ErrOverflow {
		t.Fatalf("minutesToSeconds overflow = %v", err)
	}
	if _, err := minutesToSeconds(math.MinInt64); err != ErrOverflow {
		t.Fatalf("minutesToSeconds underflow = %v", err)
	}
	if _, err := addSeconds(math.MaxInt64, 1); err != ErrOverflow {
		t.Fatalf("addSeconds overflow = %v", err)
	}
}
