package policysafe_test

import (
	"strings"
	"testing"

	"formal.local/w27-6f5ae83635/policysafe"
)

func mustBuild(t *testing.T, source string) *policysafe.Compiled {
	t.Helper()
	compiled, err := policysafe.Compile(source)
	if err != nil {
		t.Fatalf("expected policy to compile, got error: %v", err)
	}
	return compiled
}

func compileErrorContains(t *testing.T, source, want string) {
	t.Helper()
	_, err := policysafe.Compile(source)
	if err == nil {
		t.Fatalf("expected compile error containing %q, got success", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

// TestUnsafeVariables covers unbound head variables, variables that only
// appear inside a negated literal (not-scope leakage) and non-ground facts.
func TestUnsafeVariables(t *testing.T) {
	compileErrorContains(t,
		`allow(X) :- not blocked(X).`,
		"unsafe variable")

	compileErrorContains(t,
		`allow(X) :- role(Y, admin).`,
		"unsafe variable")

	compileErrorContains(t,
		`allow(X) :- user(X), not banned(Y).`,
		"unsafe variable")

	compileErrorContains(t,
		`role(alice, X).`,
		"fully ground")

	compileErrorContains(t,
		`allow(_) :- user(_).`,
		"wildcard cannot appear in a rule head")

	compiled := mustBuild(t, `
		default allow := false.
		role(alice, admin).
		blocked(bob).
		allow(U) :- role(U, admin), not blocked(U).
	`)
	decision := compiled.Authorize(nil)
	if !decision.Allowed() || decision.Reason != policysafe.ReasonAllow {
		t.Fatalf("expected allow for safe policy, got %+v", decision)
	}
}

// TestStratification covers non-stratified recursive negation while
// ensuring ordinary positive recursion remains legal.
func TestStratification(t *testing.T) {
	compileErrorContains(t,
		`p(X) :- q(X), not p(X).`,
		"non-stratified negation")

	compileErrorContains(t,
		`
		a(X) :- not b(X).
		b(X) :- not a(X).
		`,
		"non-stratified negation")

	compiled := mustBuild(t, `
		edge(a, b).
		edge(b, c).
		reach(X, Y) :- edge(X, Y).
		reach(X, Z) :- edge(X, Y), reach(Y, Z).
	`)
	bindings, err := compiled.Query(policysafe.AtomOf("reach", policysafe.C("a"), policysafe.V("X")), nil)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	got := map[string]bool{}
	for _, binding := range bindings {
		got[binding["X"]] = true
	}
	if !got["b"] || !got["c"] || len(got) != 2 {
		t.Fatalf("expected reach(a,{b,c}), got %v", got)
	}
}

// TestFailClosedDecisions covers undefined, explicit-deny, conflict and
// allow paths.
func TestFailClosedDecisions(t *testing.T) {
	t.Run("undefined defaults to deny", func(t *testing.T) {
		compiled := mustBuild(t, `role(alice, admin).`)
		decision := compiled.Authorize(nil)
		if decision.Allowed() || decision.Effect != policysafe.EffectDeny ||
			decision.Reason != policysafe.ReasonUndefinedDeny {
			t.Fatalf("expected undefined deny, got %+v", decision)
		}
	})

	t.Run("explicit deny", func(t *testing.T) {
		compiled := mustBuild(t, `
			blocked(bob).
			deny(U) :- blocked(U).
		`)
		decision := compiled.Authorize(nil)
		if decision.Allowed() || decision.Reason != policysafe.ReasonExplicitDeny {
			t.Fatalf("expected explicit deny, got %+v", decision)
		}
	})

	t.Run("allow and deny conflict tightens to deny", func(t *testing.T) {
		compiled := mustBuild(t, `
			user(alice).
			allow(X) :- user(X).
			deny(X) :- user(X).
		`)
		decision := compiled.Authorize(nil)
		if decision.Allowed() || decision.Reason != policysafe.ReasonConflictDeny {
			t.Fatalf("expected conflict deny, got %+v", decision)
		}
		if len(decision.AllowTuples) != 1 || len(decision.DenyTuples) != 1 {
			t.Fatalf("expected both tuples recorded, got %+v", decision)
		}
	})

	t.Run("matching allow with no deny", func(t *testing.T) {
		compiled := mustBuild(t, `
			role(alice, admin).
			blocked(bob).
			allow(U) :- role(U, admin), not blocked(U).
		`)
		decision := compiled.Authorize(nil)
		if !decision.Allowed() || decision.Reason != policysafe.ReasonAllow {
			t.Fatalf("expected allow, got %+v", decision)
		}
		if got := decision.AllowTuples[0][0]; got != "alice" {
			t.Fatalf("expected allow(alice), got %q", got)
		}
	})

	t.Run("zero-arity predicates and request-scoped facts", func(t *testing.T) {
		compiled := mustBuild(t, `
			default allow := false.
			allow :- open().
		`)
		closed := compiled.Authorize(nil)
		if closed.Allowed() || closed.Reason != policysafe.ReasonUndefinedDeny {
			t.Fatalf("expected undefined deny without open(), got %+v", closed)
		}
		opened := compiled.Authorize([]policysafe.Atom{policysafe.AtomOf("open")})
		if !opened.Allowed() {
			t.Fatalf("expected allow with open() fact, got %+v", opened)
		}
	})
}
