package fieldauth

import (
	"reflect"
	"strings"
	"testing"
)

// directory resolves roles from a committed directory of user records.
// For self-service updates the target record IS the actor's record, so
// roles come from the pre-image of the record being modified.
func directory(users map[string]Record) RoleResolver {
	return func(actorID string, old Record) []string {
		if old["id"] == actorID {
			if role, ok := old["role"].(string); ok {
				return []string{role}
			}
		}
		if rec, ok := users[actorID]; ok {
			if role, ok := rec["role"].(string); ok {
				return []string{role}
			}
		}
		return nil
	}
}

func hrEngine() *Engine {
	users := map[string]Record{
		"alice": {"id": "alice", "role": "user", "salary": 100},
		"bob":   {"id": "bob", "role": "admin", "salary": 200},
	}
	e := NewEngine(directory(users))
	e.Guard(
		Rule{
			ID:     "role-guard",
			Fields: []string{"role"},
			Conds:  []Cond{ActorHasRole("admin")},
		},
		Rule{
			ID:     "salary-guard",
			Fields: []string{"salary"},
			Conds:  []Cond{ActorHasRole("admin")},
		},
	)
	return e
}

// The core attack: in ONE atomic patch, alice promotes herself to admin
// and then edits a sensitive field. Authorization must use her OLD
// identity (user), so both changes are rejected.
func TestSelfPromotionAttackDenied(t *testing.T) {
	e := hrEngine()
	old := Record{"id": "alice", "role": "user", "salary": 100}

	rec, denial, err := e.Authorize("alice", old, Patch{"role": "admin", "salary": 5000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial == nil {
		t.Fatalf("expected denial, got authorized record %v", rec)
	}
	if rec != nil {
		t.Fatalf("rejected update must not return a record, got %v", rec)
	}

	var failedRules []string
	for _, f := range denial.Failures {
		failedRules = append(failedRules, f.Rule)
		if f.Src != Old {
			t.Errorf("role-based conditions must be pre-image conditions, got %v", f.Src)
		}
	}
	for _, want := range []string{"role-guard", "salary-guard"} {
		if !contains(failedRules, want) {
			t.Errorf("expected rule %q to fail, failures: %v", want, denial.Failures)
		}
	}

	// Minimal explanation: both patch fields are responsible; reverting
	// only one still leaves the other violation.
	if got := denial.MinimalFields(); !reflect.DeepEqual(got, []string{"role", "salary"}) {
		t.Errorf("MinimalFields = %v, want [role salary]", got)
	}
}

// A real admin (per the pre-image) passes the same checks.
func TestRealAdminAllowed(t *testing.T) {
	e := hrEngine()
	old := Record{"id": "bob", "role": "admin", "salary": 200}

	rec, denial, err := e.Authorize("bob", old, Patch{"salary": 9000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial != nil {
		t.Fatalf("unexpected denial: %v", denial)
	}
	if rec["salary"] != 9000 {
		t.Errorf("salary = %v, want 9000", rec["salary"])
	}
}

// An admin editing someone else's record is authorized via the
// directory, still resolved before the patch is applied.
func TestAdminEditingOtherRecord(t *testing.T) {
	e := hrEngine()
	old := Record{"id": "alice", "role": "user", "salary": 100}

	rec, denial, err := e.Authorize("bob", old, Patch{"salary": 150})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial != nil {
		t.Fatalf("unexpected denial: %v", denial)
	}
	if rec["salary"] != 150 {
		t.Errorf("salary = %v, want 150", rec["salary"])
	}
}

// Atomicity: a patch mixing an allowed and a forbidden change is
// rejected as a whole; no partial record is produced.
func TestAtomicRejection(t *testing.T) {
	e := hrEngine()
	old := Record{"id": "alice", "role": "user", "salary": 100, "nickname": "al"}

	rec, denial, err := e.Authorize("alice", old, Patch{"nickname": "ally", "salary": 5000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial == nil {
		t.Fatal("expected denial")
	}
	if rec != nil {
		t.Fatalf("atomic rejection must not return partial record, got %v", rec)
	}
	// Only the salary rule failed; the allowed nickname change is not
	// part of the explanation.
	if got := denial.MinimalFields(); !reflect.DeepEqual(got, []string{"salary"}) {
		t.Errorf("MinimalFields = %v, want [salary]", got)
	}
}

// Derived fields are recomputed automatically and their changes
// participate in checks: a user cannot cause a guarded derived field to
// change, even without touching it directly.
func TestDerivedFieldRecomputeAndGuard(t *testing.T) {
	e := NewEngine(directory(map[string]Record{
		"alice": {"id": "alice", "role": "user"},
		"bob":   {"id": "bob", "role": "admin"},
	}))
	err := e.Derive(Derived{
		Name:    "level",
		Depends: []string{"score"},
		Compute: func(rec Record) interface{} {
			if score, _ := rec["score"].(int); score >= 90 {
				return "A"
			}
			return "B"
		},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	e.Guard(Rule{
		ID:     "level-guard",
		Fields: []string{"level"},
		Conds:  []Cond{ActorHasRole("admin")},
	})

	old := Record{"id": "alice", "role": "user", "score": 80, "level": "B"}

	// User bumps score 80 -> 95, which flips derived level B -> A.
	rec, denial, err := e.Authorize("alice", old, Patch{"score": 95})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial == nil {
		t.Fatalf("expected denial: derived level change must be guarded, got %v", rec)
	}
	// Minimal explanation maps the derived violation back to the
	// patched base field that caused it.
	if got := denial.MinimalFields(); !reflect.DeepEqual(got, []string{"score"}) {
		t.Errorf("MinimalFields = %v, want [score]", got)
	}
	if got := denial.Fields(); !reflect.DeepEqual(got, []string{"level"}) {
		t.Errorf("Fields = %v, want [level]", got)
	}

	// Admin may cause the same derived change.
	oldAdmin := Record{"id": "bob", "role": "admin", "score": 80, "level": "B"}
	rec, denial, err = e.Authorize("bob", oldAdmin, Patch{"score": 95})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial != nil {
		t.Fatalf("unexpected denial: %v", denial)
	}
	if rec["level"] != "A" {
		t.Errorf("derived level = %v, want A (auto-recomputed)", rec["level"])
	}
}

// Derived fields cannot be patched directly.
func TestPatchDerivedFieldRejected(t *testing.T) {
	e := NewEngine(directory(nil))
	if err := e.Derive(Derived{
		Name:    "level",
		Depends: []string{"score"},
		Compute: func(rec Record) interface{} { return "A" },
	}); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	_, _, err := e.Authorize("alice", Record{"id": "alice"}, Patch{"level": "A"})
	if err == nil {
		t.Fatal("expected error when patching a derived field directly")
	}
}

// Conditions distinguish pre-image and post-image reads: a status
// transition is allowed only FROM draft (old) TO a known state (new).
func TestOldVsNewConditions(t *testing.T) {
	e := NewEngine(directory(nil))
	e.Guard(Rule{
		ID:     "status-transition",
		Fields: []string{"status"},
		Conds: []Cond{
			OldCond("only drafts can transition", func(old Record, _ Auth) bool {
				return old["status"] == "draft"
			}),
			NewCond("target state must be draft or published", func(new Record, _ Auth) bool {
				s := new["status"]
				return s == "draft" || s == "published"
			}),
		},
	})

	// draft -> published: allowed.
	_, denial, err := e.Authorize("alice", Record{"status": "draft"}, Patch{"status": "published"})
	if err != nil || denial != nil {
		t.Fatalf("draft->published should pass, denial=%v err=%v", denial, err)
	}

	// published -> draft: rejected on the PRE-IMAGE condition only.
	_, denial, err = e.Authorize("alice", Record{"status": "published"}, Patch{"status": "draft"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial == nil {
		t.Fatal("expected denial")
	}
	if len(denial.Failures) != 1 || denial.Failures[0].Src != Old {
		t.Fatalf("expected one pre-image failure, got %v", denial.Failures)
	}

	// draft -> archived: rejected on the POST-IMAGE condition.
	_, denial, err = e.Authorize("alice", Record{"status": "draft"}, Patch{"status": "archived"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denial == nil {
		t.Fatal("expected denial")
	}
	if len(denial.Failures) != 1 || denial.Failures[0].Src != New {
		t.Fatalf("expected one post-image failure, got %v", denial.Failures)
	}
}

// BothCond compares pre- and post-image: salary may not decrease.
func TestTransitionCondition(t *testing.T) {
	e := NewEngine(directory(nil))
	e.Guard(Rule{
		ID:     "no-salary-cut",
		Fields: []string{"salary"},
		Conds: []Cond{
			BothCond("salary must not decrease", func(old, new Record, _ Auth) bool {
				ov, _ := old["salary"].(int)
				nv, _ := new["salary"].(int)
				return nv >= ov
			}),
		},
	})

	if _, denial, _ := e.Authorize("a", Record{"salary": 100}, Patch{"salary": 120}); denial != nil {
		t.Fatalf("raise should pass: %v", denial)
	}
	_, denial, _ := e.Authorize("a", Record{"salary": 100}, Patch{"salary": 50})
	if denial == nil {
		t.Fatal("cut should be denied")
	}
	if denial.Failures[0].Src != Both {
		t.Errorf("Src = %v, want transition", denial.Failures[0].Src)
	}
}

// Untouched rules are not evaluated, and a no-op patch changes nothing.
func TestNoChangeNoChecks(t *testing.T) {
	e := hrEngine()
	old := Record{"id": "alice", "role": "user", "salary": 100}

	rec, denial, err := e.Authorize("alice", old, Patch{"salary": 100})
	if err != nil || denial != nil {
		t.Fatalf("no-op patch should pass, denial=%v err=%v", denial, err)
	}
	if !reflect.DeepEqual(rec, old.Clone()) {
		t.Errorf("rec = %v, want unchanged %v", rec, old)
	}
}

// Derived dependency cycles are rejected at registration.
func TestDerivedCycleRejected(t *testing.T) {
	e := NewEngine(directory(nil))
	err := e.Derive(
		Derived{Name: "a", Depends: []string{"b"}, Compute: func(rec Record) interface{} { return nil }},
		Derived{Name: "b", Depends: []string{"a"}, Compute: func(rec Record) interface{} { return nil }},
	)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestDenialErrorMessage(t *testing.T) {
	e := hrEngine()
	old := Record{"id": "alice", "role": "user", "salary": 100}
	_, denial, _ := e.Authorize("alice", old, Patch{"salary": 5000})
	msg := denial.Error()
	if !strings.Contains(msg, "salary-guard") || !strings.Contains(msg, "pre-image") {
		t.Errorf("unhelpful denial message: %s", msg)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
