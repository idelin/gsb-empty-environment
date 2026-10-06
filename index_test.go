package recur

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func equalInstances(a, b []Instance) bool {
	return reflect.DeepEqual(a, b)
}

// The indexed query must agree with per-week traversal after every update.
func TestIndexMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	r := mustRule(t, "prop", "2026-01-05", 52)
	ix := NewIndex(r)
	base := Revision{FromSeq: 0, Rev: 1,
		Params: Params{Weekday: time.Monday, Minute: 9 * 60, DurationMin: 30, OffsetMin: 0}}
	if err := ix.UpsertRevision(base); err != nil {
		t.Fatal(err)
	}

	check := func(tag string) {
		t.Helper()
		for q := 0; q < 25; q++ {
			from := rng.Int63n(400 * 24 * 3600)
			to := from + rng.Int63n(14*24*3600) + 60
			got := ix.Query(from, to)
			want := r.QueryReference(from, to)
			if !equalInstances(got, want) {
				t.Fatalf("%s: index/reference mismatch in [%d,%d)\n got %v\nwant %v",
					tag, from, to, ids(got), ids(want))
			}
		}
	}
	check("initial")

	wds := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	for step := 0; step < 300; step++ {
		switch rng.Intn(3) {
		case 0: // range revision
			rev := Revision{
				FromSeq: rng.Intn(52),
				Rev:     1 + rng.Intn(5),
				Params: Params{
					Weekday:     wds[rng.Intn(7)],
					Minute:      rng.Intn(1440),
					DurationMin: 1 + rng.Intn(600),
					OffsetMin:   -720 + rng.Intn(1441),
				},
			}
			if err := ix.UpsertRevision(rev); err != nil {
				t.Fatal(err)
			}
		case 1: // override
			exc := Exception{
				Seq:    rng.Intn(52),
				Rev:    1 + rng.Intn(5),
				Action: ActionOverride,
				Params: Params{
					Weekday:     wds[rng.Intn(7)],
					Minute:      rng.Intn(1440),
					DurationMin: 1 + rng.Intn(600),
					OffsetMin:   -720 + rng.Intn(1441),
				},
			}
			if err := ix.UpsertException(exc); err != nil {
				t.Fatal(err)
			}
		default: // cancel or restore
			action := ActionCancel
			if rng.Intn(2) == 0 {
				action = ActionRestore
			}
			exc := Exception{Seq: rng.Intn(52), Rev: 1 + rng.Intn(5), Action: action}
			if err := ix.UpsertException(exc); err != nil {
				t.Fatal(err)
			}
		}
		if step%10 == 9 {
			check("after random updates")
		}
	}
	check("final")
}

// Query results must come back ordered by start and respect the half-open
// interval exactly at boundaries.
func TestIndexQueryBoundaries(t *testing.T) {
	r := mustRule(t, "b", "2026-01-05", 2)
	ix := NewIndex(r)
	if err := ix.UpsertRevision(Revision{FromSeq: 0, Rev: 1,
		Params: Params{Weekday: time.Monday, Minute: 9 * 60, DurationMin: 30, OffsetMin: 0}}); err != nil {
		t.Fatal(err)
	}
	start := unix(t, "2026-01-05T09:00:00Z")
	end := unix(t, "2026-01-05T09:30:00Z")
	if got := ix.Query(end, end+3600); len(got) != 0 {
		t.Fatalf("from==end should miss, got %v", ids(got))
	}
	if got := ix.Query(end-3600, start); len(got) != 0 {
		t.Fatalf("to==start should miss, got %v", ids(got))
	}
	if got := ix.Query(start, end); len(got) != 1 {
		t.Fatalf("exact window should hit, got %v", ids(got))
	}
	if got := ix.Query(start, start); len(got) != 0 {
		t.Fatalf("empty range should miss, got %v", ids(got))
	}
}

// A revision with a smaller rev number must not disturb the index.
func TestIndexStaleRevisionIgnored(t *testing.T) {
	r := mustRule(t, "s", "2026-01-05", 2)
	ix := NewIndex(r)
	newer := Revision{FromSeq: 0, Rev: 5,
		Params: Params{Weekday: time.Wednesday, Minute: 600, DurationMin: 30, OffsetMin: 0}}
	if err := ix.UpsertRevision(newer); err != nil {
		t.Fatal(err)
	}
	stale := Revision{FromSeq: 0, Rev: 2,
		Params: Params{Weekday: time.Friday, Minute: 600, DurationMin: 30, OffsetMin: 0}}
	if err := ix.UpsertRevision(stale); err != nil {
		t.Fatal(err)
	}
	inst, ok := r.Instance(0)
	if !ok || inst.Rev != 5 {
		t.Fatalf("stale revision leaked: %+v", inst)
	}
	got := ix.Query(inst.Start-1, inst.End+1)
	if len(got) != 1 || got[0].Rev != 5 {
		t.Fatalf("index out of sync: %+v", got)
	}
}
