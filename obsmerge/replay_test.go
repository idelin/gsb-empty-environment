package obsmerge

import "testing"

func sampleObservations() []Observation[string] {
	return []Observation[string]{
		strObs("o3", "src-b", "v2", "page", "k1",
			[]string{"name"}, map[string]Nullable[string]{"name": Of("B Name")}, 3),
		strObs("o1", "src-a", "v1", "page", "k1",
			[]string{"name", "email"},
			map[string]Nullable[string]{"name": Of("A Name"), "email": Of("a@x.io")}, 1),
		strObs("o2", "src-a", "v2", "page", "k1",
			[]string{"name"}, map[string]Nullable[string]{"name": Of("A Name v2")}, 2),
		strObs("o4", "src-c", "v1", "page", "k2",
			[]string{"name"}, map[string]Nullable[string]{"name": Of("C Name")}, 4),
	}
}

func TestFoldDeterministic(t *testing.T) {
	prio := StaticPriority(0, map[string]int{"src-a": 1})
	out, err := Fold(prio, sampleObservations())
	if err != nil {
		t.Fatal(err)
	}

	k1 := out["k1"]
	name := k1.Fields["name"]
	if name.Status != StatusResolved || name.Value != "A Name v2" {
		t.Fatalf("fold name = %+v", name)
	}
	if name.Sources[0].ObservationID != "o2" {
		t.Fatalf("same-source supersession failed: %+v", name.Sources)
	}
	email := k1.Fields["email"]
	if email.Status != StatusResolved || email.Value != "a@x.io" {
		t.Fatalf("fold email = %+v", email)
	}
	if _, ok := out["k2"]; !ok {
		t.Fatal("k2 missing from fold output")
	}

	// Folding the same shuffled input again must be identical.
	again, err := Fold(prio, sampleObservations())
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(out) {
		t.Fatal("fold output length differs across runs")
	}
	for key, rec := range out {
		for field, fv := range rec.Fields {
			other := again[key].Fields[field]
			if !fieldViewsEqual(fv, other) {
				t.Fatalf("nondeterministic fold for %s/%s", key, field)
			}
		}
	}
}

func TestReplayAndVerifyMatchesIncremental(t *testing.T) {
	prio := StaticPriority(0, map[string]int{"src-a": 1})
	eng := NewEngine[string](prio)
	for _, obs := range sampleObservations() {
		if err := eng.Apply(obs); err != nil {
			t.Fatal(err)
		}
	}

	report, err := eng.ReplayAndVerify()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match {
		t.Fatalf("incremental/replay mismatch: %+v", report.Diffs)
	}
	if report.ObservationCount != 4 || report.RecordCount != 2 {
		t.Fatalf("report counts = %+v", report)
	}

	// Replay output carries provenance for every output field.
	name := report.Records["k1"].Fields["name"]
	if len(name.Sources) != 2 {
		t.Fatalf("provenance missing: %+v", name.Sources)
	}

	// Retract one observation; the recomputed key must still match replay.
	if err := eng.Retract("o2"); err != nil {
		t.Fatal(err)
	}
	report, err = eng.ReplayAndVerify()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match {
		t.Fatalf("mismatch after retract: %+v", report.Diffs)
	}
	if report.ObservationCount != 3 {
		t.Fatalf("count = %d", report.ObservationCount)
	}
	if fv := report.Records["k1"].Fields["name"]; fv.Value != "A Name" {
		t.Fatalf("post-retract replay value = %+v", fv)
	}
}

func TestReplayDetectsDrift(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	must(t, eng.Apply(strObs("o1", "src-a", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("real")}, 1)))

	// Simulate corrupted incremental state that replay must catch.
	eng.mu.Lock()
	eng.byKey["k1"].fields["name"].evidence[0].Value = "corrupt"
	eng.mu.Unlock()

	report, err := eng.ReplayAndVerify()
	if err != nil {
		t.Fatal(err)
	}
	if report.Match || len(report.Diffs) != 1 {
		t.Fatalf("expected one diff, got match=%v diffs=%+v", report.Match, report.Diffs)
	}
	diff := report.Diffs[0]
	if diff.BusinessKey != "k1" || diff.Field != "name" ||
		diff.Incremental.Value != "corrupt" || diff.Replayed.Value != "real" {
		t.Fatalf("unexpected diff: %+v", diff)
	}
}

func TestFoldRejectsInvalid(t *testing.T) {
	if _, err := Fold[string](nil, []Observation[string]{
		strObs("o1", "", "v", "p", "k", []string{"f"}, nil, 1),
	}); err == nil {
		t.Fatal("expected validation error from Fold")
	}
}
