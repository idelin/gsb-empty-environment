package obsmerge

import (
	"errors"
	"testing"
)

func strObs(id, source, version, page, key string, covered []string, fields map[string]Nullable[string], seq int64) Observation[string] {
	return Observation[string]{
		ID:            id,
		SourceID:      source,
		SourceVersion: version,
		PageType:      page,
		BusinessKey:   key,
		CoveredFields: covered,
		Fields:        fields,
		Seq:           seq,
	}
}

func fieldMust(t *testing.T, eng *Engine[string], key, field string) FieldView[string] {
	t.Helper()
	fv, err := eng.Field(key, field)
	if err != nil {
		t.Fatalf("Field(%q,%q) error: %v", key, field, err)
	}
	return fv
}

func TestPartialCoverageLeavesUnknown(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	err := eng.Apply(strObs("o1", "src-a", "v1", "summary", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Acme")}, 1))
	if err != nil {
		t.Fatal(err)
	}

	name := fieldMust(t, eng, "k1", "name")
	if name.Status != StatusResolved || name.Value != "Acme" {
		t.Fatalf("name = %+v", name)
	}
	if got := name.Sources[0]; got.SourceID != "src-a" || got.SourceVersion != "v1" || got.PageType != "summary" {
		t.Fatalf("provenance lost: %+v", got)
	}

	// email was never covered: unknown, not deleted.
	email := fieldMust(t, eng, "k1", "email")
	if email.Status != StatusUnknown {
		t.Fatalf("uncovered field status = %v, want unknown", email.Status)
	}
}

func TestCoveredEmptyDeletesButUncoveredSurvives(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	must(t, eng.Apply(strObs("o1", "src-a", "v1", "detail", "k1",
		[]string{"name", "email"},
		map[string]Nullable[string]{"name": Of("Acme"), "email": Of("a@x.io")}, 1)))

	// Second page covers name (explicitly empty) but not email.
	must(t, eng.Apply(strObs("o2", "src-a", "v2", "refresh", "k1",
		[]string{"name"},
		map[string]Nullable[string]{"name": Empty[string]()}, 2)))

	name := fieldMust(t, eng, "k1", "name")
	if name.Status != StatusDeleted {
		t.Fatalf("covered null status = %v, want deleted", name.Status)
	}
	email := fieldMust(t, eng, "k1", "email")
	if email.Status != StatusResolved || email.Value != "a@x.io" {
		t.Fatalf("uncovered field was altered: %+v", email)
	}

	// A covered field missing from the Fields map is also an explicit empty.
	must(t, eng.Apply(strObs("o3", "src-b", "v9", "refresh", "k1",
		[]string{"email"}, map[string]Nullable[string]{}, 3)))
	email = fieldMust(t, eng, "k1", "email")
	if email.Status != StatusConflict {
		// equal priority: src-a value vs src-b explicit null, both retained
		t.Fatalf("status = %v, want conflict", email.Status)
	}
	if len(email.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(email.Candidates))
	}
}

func TestPriorityMergeAndNoArrivalOverride(t *testing.T) {
	prio := StaticPriority(0, map[string]int{"official": 2, "other-official": 2, "partner": 1})
	eng := NewEngine[string](prio)

	// Low-priority source arrives first.
	must(t, eng.Apply(strObs("p1", "partner", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Partner Name")}, 1)))
	// High-priority source arrives later and must win on priority, not order.
	must(t, eng.Apply(strObs("f1", "official", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Official Name")}, 2)))

	name := fieldMust(t, eng, "k1", "name")
	if name.Status != StatusResolved || name.Value != "Official Name" {
		t.Fatalf("priority result = %+v", name)
	}
	if len(name.Sources) != 2 || name.Sources[1].SourceID != "partner" {
		t.Fatalf("lower-priority evidence not retained: %+v", name.Sources)
	}

	// Equal-priority peer arriving later must not overwrite the first value.
	must(t, eng.Apply(strObs("o2", "other-official", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Rival Name")}, 3)))
	name = fieldMust(t, eng, "k1", "name")
	if name.Status != StatusConflict {
		t.Fatalf("equal-priority status = %v, want conflict", name.Status)
	}
	if len(name.Candidates) != 2 {
		t.Fatalf("want both equal-priority candidates, got %d", len(name.Candidates))
	}
	for _, c := range name.Candidates {
		if c.Priority != 2 {
			t.Fatalf("candidate priority = %d", c.Priority)
		}
	}
}

func TestSameSourceSupersession(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	must(t, eng.Apply(strObs("o1", "src-a", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("old")}, 1)))
	must(t, eng.Apply(strObs("o2", "src-a", "v2", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("new")}, 2)))

	name := fieldMust(t, eng, "k1", "name")
	if name.Status != StatusResolved || name.Value != "new" {
		t.Fatalf("supersession result = %+v", name)
	}
	if len(name.Sources) != 1 || name.Sources[0].ObservationID != "o2" {
		t.Fatalf("stale evidence retained: %+v", name.Sources)
	}

	// Duplicate observation ID is rejected.
	err := eng.Apply(strObs("o2", "src-a", "v3", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("dup")}, 4))
	if err == nil {
		t.Fatal("duplicate observation ID should be rejected")
	}
}

func TestRetractRecomputesAffectedKey(t *testing.T) {
	prio := StaticPriority(0, map[string]int{"official": 2})
	eng := NewEngine[string](prio)

	must(t, eng.Apply(strObs("bad", "official", "v-bad", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Wrong")}, 1)))
	must(t, eng.Apply(strObs("good", "partner", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Right")}, 2)))
	must(t, eng.Apply(strObs("other", "partner", "v1", "page", "k2",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("Untouched")}, 3)))

	if fv := fieldMust(t, eng, "k1", "name"); fv.Value != "Wrong" {
		t.Fatalf("pre-retract value = %v", fv.Value)
	}

	if err := eng.Retract("bad"); err != nil {
		t.Fatal(err)
	}

	fv := fieldMust(t, eng, "k1", "name")
	if fv.Status != StatusResolved || fv.Value != "Right" {
		t.Fatalf("post-retract field = %+v", fv)
	}
	if fv.Sources[0].ObservationID != "good" {
		t.Fatalf("retracted evidence still present: %+v", fv.Sources)
	}
	other := fieldMust(t, eng, "k2", "name")
	if other.Value != "Untouched" {
		t.Fatalf("unrelated key changed: %+v", other)
	}

	if !slicesContain(eng.Observations(), "good") || slicesContain(eng.Observations(), "bad") {
		t.Fatalf("observations after retract = %v", eng.Observations())
	}
	if err := eng.Retract("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retract missing error = %v", err)
	}
}

func TestRetractLastObservationDropsKey(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	must(t, eng.Apply(strObs("o1", "src-a", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("x")}, 1)))
	must(t, eng.Retract("o1"))

	if _, err := eng.View("k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("View after full retract error = %v", err)
	}
	if len(eng.Keys()) != 0 {
		t.Fatalf("keys = %v", eng.Keys())
	}
	// Field-level read degrades to unknown rather than erroring.
	if fv := fieldMust(t, eng, "k1", "name"); fv.Status != StatusUnknown {
		t.Fatalf("status = %v", fv.Status)
	}
}

func TestRetractFallsBackToOlderSameSourceEvidence(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	must(t, eng.Apply(strObs("o1", "src-a", "v1", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("first")}, 1)))
	must(t, eng.Apply(strObs("o2", "src-a", "v2", "page", "k1",
		[]string{"name"}, map[string]Nullable[string]{"name": Of("second")}, 2)))

	if fv := fieldMust(t, eng, "k1", "name"); fv.Value != "second" {
		t.Fatalf("pre-retract = %v", fv.Value)
	}
	must(t, eng.Retract("o2"))

	fv := fieldMust(t, eng, "k1", "name")
	if fv.Status != StatusResolved || fv.Value != "first" {
		t.Fatalf("fallback after retract = %+v", fv)
	}
	if fv.Sources[0].ObservationID != "o1" || fv.Sources[0].SourceVersion != "v1" {
		t.Fatalf("wrong provenance after retract: %+v", fv.Sources)
	}

	report, err := eng.ReplayAndVerify()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match {
		t.Fatalf("replay mismatch: %+v", report.Diffs)
	}
}

func TestValidation(t *testing.T) {
	eng := NewEngine[string](StaticPriority(0, nil))
	cases := []struct {
		name string
		obs  Observation[string]
	}{
		{"missing ID", strObs("", "s", "v", "p", "k", []string{"f"}, nil, 1)},
		{"missing source", strObs("o", "", "v", "p", "k", []string{"f"}, nil, 1)},
		{"missing key", strObs("o", "s", "v", "p", "", []string{"f"}, nil, 1)},
		{"duplicate covered", strObs("o", "s", "v", "p", "k", []string{"f", "f"}, nil, 1)},
		{"field not covered", strObs("o", "s", "v", "p", "k", []string{"f"},
			map[string]Nullable[string]{"g": Of("x")}, 1)},
	}
	for _, tc := range cases {
		if err := eng.Apply(tc.obs); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

func TestPerFieldPriorityAndExplicitNullWins(t *testing.T) {
	prio := FieldPriority(0, nil, map[string]map[string]int{
		"name":  {"crm": 2},
		"email": {"billing": 2},
	})
	eng := NewEngine[string](prio)
	must(t, eng.Apply(strObs("a", "crm", "v1", "p", "k1",
		[]string{"name", "email"},
		map[string]Nullable[string]{"name": Of("CRM Name"), "email": Of("crm@x.io")}, 1)))
	must(t, eng.Apply(strObs("b", "billing", "v1", "p", "k1",
		[]string{"name", "email"},
		map[string]Nullable[string]{"name": Of("Billing Name"), "email": Empty[string]()}, 2)))

	name := fieldMust(t, eng, "k1", "name")
	if name.Value != "CRM Name" {
		t.Fatalf("name = %+v", name)
	}
	email := fieldMust(t, eng, "k1", "email")
	if email.Status != StatusDeleted {
		t.Fatalf("high-priority explicit null = %+v", email)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func slicesContain(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}
