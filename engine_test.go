package entitymerge

import (
	"testing"
)

func testRules() []Rule {
	return []Rule{
		{Name: "taxid", BlockFields: []string{"city"}, MatchFields: []string{"taxid"}},
		{Name: "namecity", BlockFields: []string{"name"}, MatchFields: []string{"name", "city"}},
		{Name: "phone", BlockFields: []string{"phone"}, MatchFields: []string{"phone"}},
	}
}

func rec(id string, fields map[string]string) Record {
	return Record{ID: id, Source: "test", Fields: fields}
}

func mustEntity(t *testing.T, e *Engine, id string) string {
	t.Helper()
	ent, ok := e.EntityOf(id)
	if !ok {
		t.Fatalf("record %s not found", id)
	}
	return ent
}

func TestTransitiveMerge(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "acme", "city": "sh", "taxid": "T1"}))
	e.AddRecord(rec("r2", map[string]string{"name": "acme", "city": "sh", "taxid": "T1"}))
	e.AddRecord(rec("r3", map[string]string{"name": "acme", "city": "sh", "taxid": "T1"}))
	for _, id := range []string{"r1", "r2", "r3"} {
		if got := mustEntity(t, e, id); got != "r1" {
			t.Fatalf("entity of %s = %s, want r1", id, got)
		}
	}
	if n := len(e.Entities()); n != 1 {
		t.Fatalf("got %d entities, want 1", n)
	}
}

func TestEntityIDIsEarliestRecord(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "acme", "city": "sh"}))
	e.AddRecord(rec("r2", map[string]string{"name": "acme", "city": "sh"}))
	if got := mustEntity(t, e, "r1"); got != "r1" {
		t.Fatalf("entity = %s, want r1", got)
	}
	e.AddRecord(rec("r0", map[string]string{"name": "acme", "city": "sh"}))
	if got := mustEntity(t, e, "r0"); got != "r1" {
		t.Fatalf("entity = %s, want r1", got)
	}
}

func TestSplitStableEntityIDs(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "acme", "city": "sh"}))
	e.AddRecord(rec("r2", map[string]string{"name": "acme", "city": "sh", "taxid": "T9"}))
	e.AddRecord(rec("r3", map[string]string{"name": "acme-ltd", "city": "sh", "taxid": "T9"}))
	if n := len(e.Entities()); n != 1 {
		t.Fatalf("got %d entities, want 1", n)
	}
	if !e.Retract("taxid", "r2", "r3") {
		t.Fatal("retract failed")
	}
	if got := mustEntity(t, e, "r1"); got != "r1" {
		t.Fatalf("entity of r1 = %s, want r1 (stable)", got)
	}
	if got := mustEntity(t, e, "r2"); got != "r1" {
		t.Fatalf("entity of r2 = %s, want r1 (stable)", got)
	}
	if got := mustEntity(t, e, "r3"); got != "r3" {
		t.Fatalf("entity of r3 = %s, want r3 (earliest of new component)", got)
	}
}

func TestMultiEvidenceSurvivesSingleRetract(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "acme", "city": "sh", "taxid": "T1"}))
	e.AddRecord(rec("r2", map[string]string{"name": "acme", "city": "sh", "taxid": "T1"}))
	if !e.Retract("taxid", "r1", "r2") {
		t.Fatal("retract failed")
	}
	if got := mustEntity(t, e, "r2"); got != "r1" {
		t.Fatalf("entity of r2 = %s, want r1 (edge still supported)", got)
	}
}

func TestBlockingSkipsComparison(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "a", "city": "sh", "taxid": "T1"}))
	e.AddRecord(rec("r2", map[string]string{"name": "b", "city": "bj", "taxid": "T1"}))
	if n := len(e.Entities()); n != 2 {
		t.Fatalf("got %d entities, want 2 (different blocks)", n)
	}
}

func TestCustomMatcher(t *testing.T) {
	rules := []Rule{{
		Name:        "fuzzy",
		BlockFields: []string{"city"},
		Matcher: func(a, b Record) bool {
			return a.Fields["email"] != "" && a.Fields["email"] == b.Fields["email"]
		},
	}}
	e := New(rules...)
	e.AddRecord(rec("r1", map[string]string{"city": "sh", "email": "a@x.com"}))
	e.AddRecord(rec("r2", map[string]string{"city": "sh", "email": "a@x.com"}))
	e.AddRecord(rec("r3", map[string]string{"city": "sh", "email": "b@x.com"}))
	if got := mustEntity(t, e, "r2"); got != "r1" {
		t.Fatalf("entity of r2 = %s, want r1", got)
	}
	if got := mustEntity(t, e, "r3"); got != "r3" {
		t.Fatalf("entity of r3 = %s, want r3", got)
	}
}

func TestRetractUnknownEvidence(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "acme", "city": "sh"}))
	if e.Retract("taxid", "r1", "r2") {
		t.Fatal("retract of nonexistent evidence should return false")
	}
}

func TestRetractPersistsAcrossRecompute(t *testing.T) {
	e := New(testRules()...)
	e.AddRecord(rec("r1", map[string]string{"name": "acme", "city": "sh"}))
	e.AddRecord(rec("r2", map[string]string{"name": "acme", "city": "sh"}))
	e.Retract("namecity", "r1", "r2")
	e.RecomputeAll()
	if n := len(e.Entities()); n != 2 {
		t.Fatalf("after recompute got %d entities, want 2 (retraction persists)", n)
	}
}
