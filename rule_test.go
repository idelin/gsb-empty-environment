package schedule

import (
	"math"
	"testing"
	"time"
)

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func query(t *testing.T, s *Scheduler, rule, from, to string) []Instance {
	t.Helper()
	got, err := s.Query(rule, ts(from), ts(to))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func newStandup(t *testing.T, s *Scheduler, weeks int) {
	t.Helper()
	must(t, s.NewRule("standup", date("2026-01-05"), weeks)) // 2026-01-05 is a Monday
	_, err := s.AddRevision("standup", Params{Rev: 1, FromWeek: 0, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)
}

// Moving the weekly meeting from Monday to Tuesday: old Monday slot must
// be empty, only the Tuesday slot hits.
func TestWeekdayMoveOnlyHitsNewDay(t *testing.T) {
	s := NewScheduler()
	newStandup(t, s, 4)
	_, err := s.AddRevision("standup", Params{Rev: 2, FromWeek: 1, Weekday: 1, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)

	if got := query(t, s, "standup", "2026-01-12T09:00:00Z", "2026-01-12T11:00:00Z"); len(got) != 0 {
		t.Fatalf("Monday window: want empty, got %+v", got)
	}
	got := query(t, s, "standup", "2026-01-13T09:00:00Z", "2026-01-13T11:00:00Z")
	if len(got) != 1 || got[0].ID != "standup#1" {
		t.Fatalf("Tuesday window: want standup#1, got %+v", got)
	}
	if !got[0].Start.Equal(ts("2026-01-13T10:00:00Z")) || got[0].Rev != 2 {
		t.Fatalf("unexpected instance %+v", got[0])
	}
	// Week 0 still uses revision 1 on Monday.
	got = query(t, s, "standup", "2026-01-05T09:00:00Z", "2026-01-05T11:00:00Z")
	if len(got) != 1 || got[0].ID != "standup#0" || got[0].Rev != 1 {
		t.Fatalf("week 0: want standup#0 rev 1, got %+v", got)
	}
}

// An instance crossing local midnight intersects windows on both days,
// and the half-open interval test excludes exact boundary touches.
func TestCrossMidnightAndHalfOpen(t *testing.T) {
	s := NewScheduler()
	must(t, s.NewRule("late", date("2026-01-05"), 2))
	_, err := s.AddRevision("late", Params{Rev: 1, FromWeek: 0, Weekday: 0, MinuteOfDay: 23*60 + 30, DurationMin: 60, OffsetMin: 0})
	must(t, err)
	// Instance: 2026-01-05 23:30 -> 2026-01-06 00:30 UTC.
	if got := query(t, s, "late", "2026-01-06T00:00:00Z", "2026-01-06T01:00:00Z"); len(got) != 1 {
		t.Fatalf("next-day window: want 1 hit, got %+v", got)
	}
	if got := query(t, s, "late", "2026-01-05T22:30:00Z", "2026-01-05T23:30:00Z"); len(got) != 0 {
		t.Fatalf("window ending at start: want empty (half-open), got %+v", got)
	}
	if got := query(t, s, "late", "2026-01-06T00:30:00Z", "2026-01-06T01:30:00Z"); len(got) != 0 {
		t.Fatalf("window starting at end: want empty (half-open), got %+v", got)
	}
}

// Changing the fixed UTC offset shifts the UTC instant but never the
// identity, and date->week conversion stays anchored at the rule start.
func TestOffsetChangeKeepsIdentity(t *testing.T) {
	s := NewScheduler()
	must(t, s.NewRule("off", date("2026-01-05"), 4))
	_, err := s.AddRevision("off", Params{Rev: 1, FromWeek: 0, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)
	before := query(t, s, "off", "2026-01-12T00:00:00Z", "2026-01-13T00:00:00Z")
	if len(before) != 1 || !before[0].Start.Equal(ts("2026-01-12T10:00:00Z")) {
		t.Fatalf("before offset change: %+v", before)
	}

	_, err = s.AddRevision("off", Params{Rev: 2, FromWeek: 1, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 480})
	must(t, err)
	after := query(t, s, "off", "2026-01-12T00:00:00Z", "2026-01-13T00:00:00Z")
	if len(after) != 1 {
		t.Fatalf("after offset change: %+v", after)
	}
	if after[0].ID != before[0].ID {
		t.Fatalf("identity drifted: %q -> %q", before[0].ID, after[0].ID)
	}
	if !after[0].Start.Equal(ts("2026-01-12T02:00:00Z")) { // 10:00 at +08:00
		t.Fatalf("offset not applied: %v", after[0].Start)
	}
	// Date->week conversion is offset-independent.
	w, err := s.WeekOfDate("off", date("2026-01-12"))
	if err != nil || w != 1 {
		t.Fatalf("WeekOfDate = %d, %v; want 1", w, err)
	}
}

// Same FromWeek: the revision with the greater revision number wins.
func TestSameFromWeekHigherRevWins(t *testing.T) {
	s := NewScheduler()
	must(t, s.NewRule("tie", date("2026-01-05"), 2))
	_, err := s.AddRevision("tie", Params{Rev: 1, FromWeek: 0, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)
	_, err = s.AddRevision("tie", Params{Rev: 3, FromWeek: 0, Weekday: 2, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)
	_, err = s.AddRevision("tie", Params{Rev: 2, FromWeek: 0, Weekday: 4, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)

	if got := query(t, s, "tie", "2026-01-05T00:00:00Z", "2026-01-06T00:00:00Z"); len(got) != 0 {
		t.Fatalf("Monday: want empty, got %+v", got)
	}
	got := query(t, s, "tie", "2026-01-07T09:00:00Z", "2026-01-07T11:00:00Z") // Wednesday
	if len(got) != 1 || got[0].Rev != 3 {
		t.Fatalf("Wednesday: want rev 3 hit, got %+v", got)
	}
}

// A single-instance override moves one occurrence out of the query
// boundary; it must disappear from the old window and appear in the new.
func TestOverrideBeyondQueryBoundary(t *testing.T) {
	s := NewScheduler()
	newStandup(t, s, 4)
	_, err := s.SetOverride("standup", 1, Params{Rev: 9, Weekday: 4, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)

	if got := query(t, s, "standup", "2026-01-12T00:00:00Z", "2026-01-16T00:00:00Z"); len(got) != 0 {
		t.Fatalf("Mon-Fri window: want empty, got %+v", got)
	}
	got := query(t, s, "standup", "2026-01-16T09:00:00Z", "2026-01-16T11:00:00Z")
	if len(got) != 1 || got[0].ID != "standup#1" || got[0].Rev != 9 {
		t.Fatalf("Friday window: want standup#1 rev 9, got %+v", got)
	}
}

// Cancellation beats override; restore removes the cancellation mark and
// the overridden instance comes back.
func TestCancelAndRestore(t *testing.T) {
	s := NewScheduler()
	newStandup(t, s, 4)
	_, err := s.SetOverride("standup", 1, Params{Rev: 9, Weekday: 4, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)
	_, err = s.Cancel("standup", 1)
	must(t, err)
	if got := query(t, s, "standup", "2026-01-12T00:00:00Z", "2026-01-19T00:00:00Z"); len(got) != 0 {
		t.Fatalf("canceled week: want empty, got %+v", got)
	}
	_, err = s.Restore("standup", 1)
	must(t, err)
	got := query(t, s, "standup", "2026-01-16T09:00:00Z", "2026-01-16T11:00:00Z")
	if len(got) != 1 || got[0].ID != "standup#1" || got[0].Rev != 9 {
		t.Fatalf("restored week: want overridden instance, got %+v", got)
	}
}

// Instances never escape the finite week count.
func TestFiniteWeeks(t *testing.T) {
	s := NewScheduler()
	newStandup(t, s, 2) // weeks 0 and 1 only
	if got := query(t, s, "standup", "2026-01-19T00:00:00Z", "2026-02-01T00:00:00Z"); len(got) != 0 {
		t.Fatalf("beyond finite horizon: want empty, got %+v", got)
	}
	if _, err := s.AddRevision("standup", Params{Rev: 2, FromWeek: 2, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0}); err == nil {
		t.Fatal("from_week beyond horizon: want error")
	}
}

func TestValidation(t *testing.T) {
	s := NewScheduler()
	if err := s.NewRule("bad", date("2026-01-06"), 4); err == nil { // Tuesday
		t.Fatal("non-Monday start: want error")
	}
	if err := s.NewRule("bad", date("2026-01-05"), 0); err == nil {
		t.Fatal("zero weeks: want error")
	}
	newStandup(t, s, 4)
	cases := []Params{
		{Rev: 2, FromWeek: 0, Weekday: 7, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0},
		{Rev: 2, FromWeek: 0, Weekday: 0, MinuteOfDay: 1440, DurationMin: 60, OffsetMin: 0},
		{Rev: 2, FromWeek: 0, Weekday: 0, MinuteOfDay: -1, DurationMin: 60, OffsetMin: 0},
		{Rev: 2, FromWeek: 0, Weekday: 0, MinuteOfDay: 600, DurationMin: 0, OffsetMin: 0},
		{Rev: 2, FromWeek: 0, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 15 * 60},
	}
	for i, p := range cases {
		if _, err := s.AddRevision("standup", p); err == nil {
			t.Fatalf("case %d: want validation error", i)
		}
	}
}

func TestMinutesConversionOverflow(t *testing.T) {
	if _, err := minutesToDuration(math.MaxInt32); err != ErrOverflow {
		t.Fatalf("duration: want ErrOverflow, got %v", err)
	}
	if _, err := minutesToSeconds(math.MaxInt64/60 + 1); err != ErrOverflow {
		t.Fatalf("seconds: want ErrOverflow, got %v", err)
	}
	if d, err := minutesToDuration(90); err != nil || d != 90*time.Minute {
		t.Fatalf("90min = %v, %v", d, err)
	}
}

// Diff between versions: time changes, removals (cancel) and additions
// (restore) are all reported against stable identities.
func TestDiffVersions(t *testing.T) {
	s := NewScheduler()
	newStandup(t, s, 4) // v1
	v2, err := s.AddRevision("standup", Params{Rev: 2, FromWeek: 1, Weekday: 1, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)
	v3, err := s.Cancel("standup", 2)
	must(t, err)
	v4, err := s.Restore("standup", 2)
	must(t, err)

	changes, err := s.Diff("standup", 1, v2)
	must(t, err)
	if len(changes) != 3 {
		t.Fatalf("v1->v2: want 3 changed, got %+v", changes)
	}
	for _, c := range changes {
		if c.Kind != Changed || c.Old == nil || c.New == nil {
			t.Fatalf("v1->v2: want changed, got %+v", c)
		}
		if c.Old.Start.Weekday() != time.Monday || c.New.Start.Weekday() != time.Tuesday {
			t.Fatalf("v1->v2: want Mon->Tue, got %v -> %v", c.Old.Start, c.New.Start)
		}
	}

	changes, err = s.Diff("standup", v2, v3)
	must(t, err)
	if len(changes) != 1 || changes[0].Kind != Removed || changes[0].ID != "standup#2" {
		t.Fatalf("v2->v3: want removed standup#2, got %+v", changes)
	}

	changes, err = s.Diff("standup", v3, v4)
	must(t, err)
	if len(changes) != 1 || changes[0].Kind != Added || changes[0].ID != "standup#2" {
		t.Fatalf("v3->v4: want added standup#2, got %+v", changes)
	}

	changes, err = s.Diff("standup", v2, v4)
	must(t, err)
	if len(changes) != 0 {
		t.Fatalf("v2->v4: want no changes, got %+v", changes)
	}
	if _, err := s.Diff("standup", 1, 99); err == nil {
		t.Fatal("unknown version: want error")
	}
}
