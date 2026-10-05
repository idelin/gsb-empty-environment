package schedule

import (
	"math/rand"
	"testing"
	"time"
)

func sameInstances(a, b []Instance) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(i Instance) string {
		return i.ID + "@" + i.Start.Format(time.RFC3339Nano)
	}
	seen := make(map[string]int, len(a))
	for _, x := range a {
		seen[key(x)]++
	}
	for _, x := range b {
		k := key(x)
		seen[k]--
		if seen[k] < 0 {
			return false
		}
	}
	return true
}

// The updatable index must agree with the naive week-by-week reference
// implementation under a random sequence of edits and queries.
func TestIndexMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := NewScheduler()
	must(t, s.NewRule("r", date("2026-01-05"), 30))
	_, err := s.AddRevision("r", Params{Rev: 1, FromWeek: 0, Weekday: 0, MinuteOfDay: 600, DurationMin: 60, OffsetMin: 0})
	must(t, err)

	base := date("2026-01-01")
	rev := 1
	check := func() {
		t.Helper()
		for q := 0; q < 6; q++ {
			qs := base.Add(time.Duration(rng.Intn(32*7*24)) * time.Hour)
			qe := qs.Add(time.Duration(1+rng.Intn(96)) * time.Hour)
			got, err := s.Query("r", qs, qe)
			must(t, err)
			want, err := s.ReferenceQuery("r", qs, qe)
			must(t, err)
			if !sameInstances(got, want) {
				t.Fatalf("index != naive for [%v, %v)\nindex: %+v\nnaive: %+v", qs, qe, got, want)
			}
		}
	}

	for i := 0; i < 300; i++ {
		week := rng.Intn(30)
		switch rng.Intn(4) {
		case 0:
			rev++
			_, err = s.AddRevision("r", Params{
				Rev:         rev,
				FromWeek:    week,
				Weekday:     rng.Intn(7),
				MinuteOfDay: rng.Intn(1440),
				DurationMin: 15 + rng.Intn(24*60),
				OffsetMin:   -720 + 30*rng.Intn(49),
			})
			must(t, err)
		case 1:
			rev++
			_, err = s.SetOverride("r", week, Params{
				Rev:         rev,
				Weekday:     rng.Intn(7),
				MinuteOfDay: rng.Intn(1440),
				DurationMin: 15 + rng.Intn(24*60),
				OffsetMin:   -720 + 30*rng.Intn(49),
			})
			must(t, err)
		case 2:
			_, err = s.Cancel("r", week)
			must(t, err)
		case 3:
			_, err = s.Restore("r", week)
			must(t, err)
		}
		check()
	}
}

// Long-duration instances started before the window must still be found
// (the index scans back by maxDur).
func TestIndexFindsLongRunningInstance(t *testing.T) {
	s := NewScheduler()
	must(t, s.NewRule("long", date("2026-01-05"), 4))
	_, err := s.AddRevision("long", Params{Rev: 1, FromWeek: 0, Weekday: 0, MinuteOfDay: 0, DurationMin: 3 * 24 * 60, OffsetMin: 0})
	must(t, err)
	got := query(t, s, "long", "2026-01-07T12:00:00Z", "2026-01-07T13:00:00Z")
	if len(got) != 1 || got[0].ID != "long#0" {
		t.Fatalf("want long#0 mid-duration hit, got %+v", got)
	}
}
