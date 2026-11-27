package policysafe_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"formal.local/w27-6f5ae83635/policysafe"
)

// TestCounterexampleSearch exercises the bounded search against the
// host-declared forbidden scenarios.
func TestCounterexampleSearch(t *testing.T) {
	compiled := mustBuild(t, `
		role(alice, admin).
		role(bob, member).
		grant(admin, read).
		grant(member, read).
		blocked(nobody).
		allow(Subj, Act) :- subject(Subj), role(Subj, R), grant(R, Act), not blocked(Subj).
		deny(Subj) :- anon(Subj).
	`)

	t.Run("anonymous write is reachable via two facts", func(t *testing.T) {
		scenario := policysafe.Scenario{
			Name: "anonymous-subject-write",
			Goal: policysafe.AtomOf("allow", policysafe.V("Subj"), policysafe.C("write")),
			FixedFacts: []policysafe.Atom{
				policysafe.AtomOf("subject", policysafe.C("attacker")),
				policysafe.AtomOf("anon", policysafe.C("attacker")),
			},
			CandidateFacts: []policysafe.Atom{
				policysafe.AtomOf("role", policysafe.C("attacker"), policysafe.C("admin")),
				policysafe.AtomOf("role", policysafe.C("attacker"), policysafe.C("member")),
				policysafe.AtomOf("grant", policysafe.C("admin"), policysafe.C("write")),
				policysafe.AtomOf("grant", policysafe.C("member"), policysafe.C("write")),
			},
		}
		counterexample, err := compiled.FindCounterexample(scenario, policysafe.SearchOptions{})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if counterexample == nil {
			t.Fatal("expected counterexample for anonymous write")
		}
		if counterexample.Size != 2 {
			t.Fatalf("expected 2 minimal facts, got %d (%v)", counterexample.Size, counterexample.Facts)
		}
		if counterexample.Bindings["Subj"] != "attacker" {
			t.Fatalf("expected attacker binding, got %v", counterexample.Bindings)
		}
		if !counterexample.AlsoDenied {
			t.Fatal("expected the counterexample world to also trigger deny")
		}
	})

	t.Run("search budget is bounded", func(t *testing.T) {
		scenario := policysafe.Scenario{
			Name: "budgeted",
			Goal: policysafe.AtomOf("allow", policysafe.V("Subj"), policysafe.C("delete")),
			CandidateFacts: []policysafe.Atom{
				policysafe.AtomOf("role", policysafe.C("x"), policysafe.C("admin")),
				policysafe.AtomOf("grant", policysafe.C("admin"), policysafe.C("delete")),
			},
		}
		_, err := compiled.FindCounterexample(scenario, policysafe.SearchOptions{MaxEvaluations: 1})
		if !errors.Is(err, policysafe.ErrSearchLimit) {
			t.Fatalf("expected ErrSearchLimit, got %v", err)
		}
	})

	t.Run("derived predicates cannot be injected", func(t *testing.T) {
		scenario := policysafe.Scenario{
			Name:           "bad-input",
			Goal:           policysafe.AtomOf("allow", policysafe.C("x"), policysafe.C("read")),
			CandidateFacts: []policysafe.Atom{policysafe.AtomOf("allow", policysafe.C("x"), policysafe.C("read"))},
		}
		_, err := compiled.FindCounterexample(scenario, policysafe.SearchOptions{})
		if err == nil || !strings.Contains(err.Error(), "derived predicate") {
			t.Fatalf("expected derived-predicate error, got %v", err)
		}
	})
}

func TestCrossTenantCounterexample(t *testing.T) {
	compiled := mustBuild(t, `
		owns(alice, t1).
		allow(Subj, Res) :- owns(Subj, T), res_tenant(Res, T).
	`)

	counterexample, err := compiled.FindCounterexample(policysafe.Scenario{
		Name:       "cross-tenant-access",
		Goal:       policysafe.AtomOf("allow", policysafe.C("alice"), policysafe.V("Res")),
		FixedFacts: []policysafe.Atom{policysafe.AtomOf("res_tenant", policysafe.C("billing"), policysafe.C("t2"))},
		CandidateFacts: []policysafe.Atom{
			policysafe.AtomOf("owns", policysafe.C("alice"), policysafe.C("t2")),
			policysafe.AtomOf("owns", policysafe.C("bob"), policysafe.C("t2")),
		},
	}, policysafe.SearchOptions{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if counterexample == nil || counterexample.Size != 1 {
		t.Fatalf("expected single-fact cross-tenant counterexample, got %+v", counterexample)
	}
	if got := counterexample.Facts[0].String(); got != "owns(alice, t2)" {
		t.Fatalf("unexpected minimal fact: %s", got)
	}

	safe, err := compiled.FindCounterexample(policysafe.Scenario{
		Name:           "no-alice-cross-tenant",
		Goal:           policysafe.AtomOf("allow", policysafe.C("alice"), policysafe.W()),
		FixedFacts:     []policysafe.Atom{policysafe.AtomOf("res_tenant", policysafe.C("billing"), policysafe.C("t2"))},
		CandidateFacts: []policysafe.Atom{policysafe.AtomOf("owns", policysafe.C("bob"), policysafe.C("t2"))},
	}, policysafe.SearchOptions{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if safe != nil {
		t.Fatalf("expected no counterexample, got %+v", safe)
	}

	violations, err := compiled.Verify([]policysafe.Scenario{
		{
			Name:           "clean",
			Goal:           policysafe.AtomOf("allow", policysafe.C("alice"), policysafe.W()),
			FixedFacts:     []policysafe.Atom{policysafe.AtomOf("res_tenant", policysafe.C("billing"), policysafe.C("t2"))},
			CandidateFacts: []policysafe.Atom{policysafe.AtomOf("owns", policysafe.C("bob"), policysafe.C("t2"))},
		},
	}, policysafe.SearchOptions{})
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected zero violations, got %v", violations)
	}
}

func TestAlreadyDerivableCounterexampleIsEmpty(t *testing.T) {
	compiled := mustBuild(t, `
		user(alice).
		allow(X) :- user(X).
	`)
	counterexample, err := compiled.FindCounterexample(policysafe.Scenario{
		Name:           "trivially-open",
		Goal:           policysafe.AtomOf("allow", policysafe.V("X")),
		CandidateFacts: []policysafe.Atom{policysafe.AtomOf("user", policysafe.C("bob"))},
	}, policysafe.SearchOptions{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if counterexample == nil || counterexample.Size != 0 || len(counterexample.Facts) != 0 {
		t.Fatalf("expected empty minimal counterexample, got %+v", counterexample)
	}
}

func TestParserRejectsBadSyntax(t *testing.T) {
	bad := []string{
		`allow(X) :- role(X, admin)`,  // missing dot
		`Allow(X) :- role(X, admin).`, // uppercase predicate
		`allow(X) role(X, admin).`,    // missing :-
		`default allow := maybe.`,     // non-boolean default
		`role(alice, "unterminated).`, // bad string
	}
	for _, source := range bad {
		if _, err := policysafe.Compile(source); err == nil {
			t.Fatalf("expected parse error for %q", source)
		}
	}
}

func TestArityConflictRejected(t *testing.T) {
	_, err := policysafe.Compile(`
		role(alice, admin).
		allow(X) :- role(X, Y, Z).
	`)
	if err == nil || !strings.Contains(err.Error(), "conflicting arities") {
		t.Fatalf("expected arity conflict error, got %v", err)
	}
}

func ExampleCompile() {
	compiled, err := policysafe.Compile(`
		default allow := false.
		role(alice, admin).
		blocked(bob).
		allow(Subj) :- role(Subj, admin), not blocked(Subj).
		deny(Subj) :- blocked(Subj), role(Subj, _).
	`)
	if err != nil {
		panic(err)
	}
	decision := compiled.Authorize(nil)
	fmt.Println(decision.Allowed(), string(decision.Reason))
	// Output:
	// true allow_matched
}
