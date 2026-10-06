package recur

import (
	"testing"
	"time"
)

func buildDiffRules(t *testing.T) (oldR, newR *Rule) {
	t.Helper()
	oldR = mustRule(t, "d", "2026-01-05", 4)
	mustRev(t, oldR, 0, 1, time.Monday, 9*60, 30, 0)

	newR = mustRule(t, "d", "2026-01-05", 5) // one extra week -> added
	mustRev(t, newR, 0, 1, time.Monday, 9*60, 30, 0)
	mustRev(t, newR, 2, 2, time.Tuesday, 10*60, 30, 0) // time change from seq 2
	if err := newR.UpsertException(Exception{Seq: 1, Rev: 1, Action: ActionCancel}); err != nil {
		t.Fatal(err)
	}
	return oldR, newR
}

func TestDiffRules(t *testing.T) {
	oldR, newR := buildDiffRules(t)
	d := DiffRules(oldR, newR)

	if len(d.Added) != 1 || d.Added[0].ID != "d#4" {
		t.Fatalf("added = %v", ids(d.Added))
	}
	if len(d.Removed) != 1 || d.Removed[0].ID != "d#1" {
		t.Fatalf("removed = %v", ids(d.Removed))
	}
	if len(d.Changed) != 2 || d.Changed[0].ID != "d#2" || d.Changed[1].ID != "d#3" {
		t.Fatalf("changed = %v", d.Changed)
	}
	c := d.Changed[0]
	if c.Old.Start != unix(t, "2026-01-19T09:00:00Z") || c.New.Start != unix(t, "2026-01-20T10:00:00Z") {
		t.Fatalf("change times wrong: %v -> %v", FormatTime(c.Old.Start), FormatTime(c.New.Start))
	}
	if c.Old.Rev != 1 || c.New.Rev != 2 {
		t.Fatalf("change revs wrong: %d -> %d", c.Old.Rev, c.New.Rev)
	}
}

func TestDiffIdentical(t *testing.T) {
	r1 := mustRule(t, "same", "2026-01-05", 3)
	mustRev(t, r1, 0, 1, time.Monday, 9*60, 30, 0)
	r2 := mustRule(t, "same", "2026-01-05", 3)
	mustRev(t, r2, 0, 1, time.Monday, 9*60, 30, 0)
	d := DiffRules(r1, r2)
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("identical rules should diff empty: %+v", d)
	}
}
