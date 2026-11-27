package txn

import "testing"

// TestConflictsReplaceMode prunes the reducible propagated stop job, keeping
// the hard-dependency start job and its anchor.
func TestConflictsReplaceMode(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Requires: []string{"b.service", "c.service"}},
		&Unit{Name: "b.service"},
		&Unit{Name: "c.service", Conflicts: []string{"b.service"}},
	)
	_, err := Solve(g, Snapshot{"b.service": true}, Request{Unit: "a.service", Job: JobStart, Mode: ModeReplace})
	assertErrorIs(t, err, ErrConflict)

	// With a reducible Wants pull instead of Requires, the start is prunable.
	g2 := graphOf(
		&Unit{Name: "a.service", Wants: []string{"b.service"}, Requires: []string{"c.service"}},
		&Unit{Name: "b.service"},
		&Unit{Name: "c.service", Conflicts: []string{"b.service"}},
	)
	plan, err := Solve(g2, Snapshot{"b.service": true}, Request{Unit: "a.service", Job: JobStart, Mode: ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "a.service", JobStart)
	assertJob(t, plan, "b.service", JobStop)
}

// TestWantsConflictStoppedInReplace shows that a reducible Wants-start is
// replaced by a propagated conflict stop, while the same scenario fails under
// fail mode.
func TestWantsConflictStoppedInReplace(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Wants: []string{"b.service"}, Requires: []string{"c.service"}},
		&Unit{Name: "b.service"},
		&Unit{Name: "c.service", Conflicts: []string{"b.service"}},
	)
	_, err := Solve(g, Snapshot{"b.service": true}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	assertErrorIs(t, err, ErrConflict)

	plan, err := Solve(g, Snapshot{"b.service": true}, Request{Unit: "a.service", Job: JobStart, Mode: ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "a.service", JobStart)
	assertJob(t, plan, "b.service", JobStop)
}

// TestReplaceIrreversiblyReplacesIrreducible demonstrates that a non-anchor
// irreducible job can be replaced only in replace-irreversibly mode.
func TestReplaceIrreversiblyReplacesIrreducible(t *testing.T) {
	g := graphOf(
		&Unit{Name: "anchor.service", Requires: []string{"a.service", "c.service"}},
		&Unit{Name: "a.service", Requires: []string{"b.service"}},
		&Unit{Name: "b.service"},
		&Unit{Name: "c.service", Conflicts: []string{"b.service"}},
	)

	// b enters as an irreducible start (anchor -> a -> b via Requires), while
	// the active symmetric conflict emits a reducible stop(b).
	_, err := Solve(g, Snapshot{"b.service": true},
		Request{Unit: "anchor.service", Job: JobStart, Mode: ModeReplace})
	assertErrorIs(t, err, ErrConflict)

	plan, err := Solve(g, Snapshot{"b.service": true},
		Request{Unit: "anchor.service", Job: JobStart, Mode: ModeReplaceIrreversibly})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "anchor.service", JobStart)
	assertJob(t, plan, "b.service", JobStop)
}

// TestAnchorNeverReplaced guarantees the anchor survives every mode even when
// a propagated stop collides with it.
func TestAnchorNeverReplaced(t *testing.T) {
	g := graphOf(
		&Unit{Name: "anchor.service", Conflicts: []string{"other.service"}},
		&Unit{Name: "other.service", Wants: []string{"anchor.service"}},
	)
	// other is started by its Wants pull and its symmetric conflict stops the
	// active anchor; the anchor must be protected.
	plan, err := Solve(g, Snapshot{"anchor.service": true},
		Request{Unit: "anchor.service", Job: JobStart, Mode: ModeReplaceIrreversibly})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "anchor.service", JobStart)
}

// TestIsolateStopsForeignUnits keeps the anchor closure reachable and stops
// every other active unit, honoring IgnoreOnIsolate.
func TestIsolateStopsForeignUnits(t *testing.T) {
	g := graphOf(
		&Unit{Name: "target.service", Requires: []string{"dep.service"}},
		&Unit{Name: "dep.service"},
		&Unit{Name: "foreign.service"},
		&Unit{Name: "kept.service", IgnoreOnIsolate: true},
	)
	snap := Snapshot{"dep.service": true, "foreign.service": true, "kept.service": true}
	plan, err := Solve(g, snap, Request{Unit: "target.service", Job: JobStart, Isolate: true, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "target.service", JobStart)
	assertJob(t, plan, "dep.service", JobStart)
	assertJob(t, plan, "foreign.service", JobStop)
	assertNoJob(t, plan, "kept.service")
}

// TestIsolateRejectsNonStart ensures isolate is only accepted with start.
func TestIsolateRejectsNonStart(t *testing.T) {
	g := graphOf(&Unit{Name: "a.service"})
	_, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStop, Isolate: true, Mode: ModeFail})
	assertErrorIs(t, err, ErrInvalidRequest)
}
