package policysafe_test

import "testing"

import "formal.local/w27-6f5ae83635/policysafe"

// TestNegationSemantics verifies that 'not' is a pure local test: it never
// introduces bindings, it is stratified against derived predicates, and it
// succeeds when the referenced relation is empty.
func TestNegationSemantics(t *testing.T) {
	compiled := mustBuild(t, `
		user(alice).
		user(bob).
		user(carol).
		banned(bob).
		active(U) :- user(U), not banned(U).
		admin(U) :- active(U), not guest(U).
	`)

	bindings, err := compiled.Query(policysafe.AtomOf("active", policysafe.V("U")), nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	got := map[string]bool{}
	for _, binding := range bindings {
		got[binding["U"]] = true
	}
	if len(got) != 2 || !got["alice"] || !got["carol"] || got["bob"] {
		t.Fatalf("expected active={alice,carol}, got %v", got)
	}

	// guest/1 has no facts at all: negation over an empty relation still
	// succeeds, and every active user becomes admin.
	admins, err := compiled.Query(policysafe.AtomOf("admin", policysafe.V("U")), nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(admins) != 2 {
		t.Fatalf("expected 2 admins over empty guest relation, got %v", admins)
	}

	// Request-scoped facts participate in negation as well.
	session := []policysafe.Atom{
		policysafe.AtomOf("guest", policysafe.C("alice")),
	}
	admins, err = compiled.Query(policysafe.AtomOf("admin", policysafe.V("U")), session)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(admins) != 1 || admins[0]["U"] != "carol" {
		t.Fatalf("expected only carol to remain admin, got %v", admins)
	}
}

func TestFailClosedOnEvaluationError(t *testing.T) {
	compiled := mustBuild(t, `
		allow(X) :- user(X).
	`)
	// A non-ground session atom cannot be inserted: the host still gets a
	// deny rather than an accidental open interface.
	decision := compiled.Authorize([]policysafe.Atom{
		policysafe.AtomOf("user", policysafe.V("X")),
	})
	if decision.Allowed() || decision.Effect != policysafe.EffectDeny || decision.Err == nil {
		t.Fatalf("expected fail-closed deny with error, got %+v", decision)
	}
}
