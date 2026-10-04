package ambcheck

import (
	"reflect"
	"strings"
	"testing"
)

func g(terms, nonterms, start string, rules ...Rule) *Grammar {
	return &Grammar{
		Terminals:    splitWS(terms),
		NonTerminals: splitWS(nonterms),
		Start:        start,
		Rules:        rules,
	}
}

func r(lhs, rhs string) Rule { return Rule{LHS: lhs, RHS: splitWS(rhs)} }

func splitWS(s string) []string {
	var out []string
	return append(out, strings.Fields(s)...)
}

func check(t *testing.T, gr *Grammar, maxLen int) *Result {
	t.Helper()
	res, err := Check(gr, Options{MaxLength: maxLen, Budget: 5_000_000})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.BudgetExceeded {
		t.Fatalf("budget unexpectedly exceeded after %d states", res.StatesExpanded)
	}
	if res.Ambiguous {
		if err := res.Verify(gr); err != nil {
			t.Fatalf("returned trees fail independent verification: %v", err)
		}
		if res.Tree1.Canonical() == res.Tree2.Canonical() {
			t.Fatalf("returned trees are structurally identical")
		}
	}
	return res
}

func TestKnownGrammars(t *testing.T) {
	cases := []struct {
		name   string
		gr     *Grammar
		maxLen int
		want   []string // nil means: no ambiguity expected within maxLen
	}{
		{
			name:   "classic expression ambiguity",
			gr:     g("a +", "E", "E", r("E", "E + E"), r("E", "a")),
			maxLen: 6,
			want:   []string{"a", "+", "a", "+", "a"},
		},
		{
			name:   "right recursion is unambiguous",
			gr:     g("a b", "S", "S", r("S", "a S"), r("S", "b")),
			maxLen: 8,
		},
		{
			name:   "left recursion is unambiguous",
			gr:     g("a b", "A", "A", r("A", "A a"), r("A", "b")),
			maxLen: 8,
		},
		{
			name:   "duplicate rules are not ambiguity",
			gr:     g("a", "S", "S", r("S", "a"), r("S", "a")),
			maxLen: 4,
		},
		{
			name:   "duplicate rules with nonterminals are not ambiguity",
			gr:     g("a b", "S A B", "S", r("S", "A B"), r("S", "A B"), r("A", "a"), r("B", "b")),
			maxLen: 4,
		},
		{
			name:   "same string from two nonterminals",
			gr:     g("a", "S A B", "S", r("S", "A"), r("S", "B"), r("A", "a"), r("B", "a")),
			maxLen: 3,
			want:   []string{"a"},
		},
		{
			name:   "shared subtree through different paths",
			gr:     g("a", "S A B C", "S", r("S", "A"), r("S", "B"), r("A", "C"), r("B", "C"), r("C", "a")),
			maxLen: 3,
			want:   []string{"a"},
		},
		{
			name:   "unit cycle gives infinitely many trees",
			gr:     g("a", "S", "S", r("S", "S"), r("S", "a")),
			maxLen: 2,
			want:   []string{"a"},
		},
		{
			name:   "mutual unit cycle",
			gr:     g("a", "S A", "S", r("S", "A"), r("A", "S"), r("A", "a")),
			maxLen: 2,
			want:   []string{"a"},
		},
		{
			name:   "S -> S S | a",
			gr:     g("a", "S", "S", r("S", "S S"), r("S", "a")),
			maxLen: 4,
			want:   []string{"a", "a", "a"},
		},
		{
			name: "terminal order breaks ties",
			gr: g("a b", "S A B C D E F", "S",
				r("S", "A B"), r("S", "C"), r("S", "D E"), r("S", "F"),
				r("A", "a"), r("B", "b"), r("C", "a b"),
				r("D", "b"), r("E", "a"), r("F", "b a")),
			maxLen: 3,
			want:   []string{"a", "b"},
		},
		{
			name:   "no derivation at all",
			gr:     g("a", "S A", "S", r("S", "A A"), r("A", "A A")),
			maxLen: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := check(t, tc.gr, tc.maxLen)
			if tc.want == nil {
				if res.Ambiguous {
					t.Fatalf("expected unambiguous, got %q", res.String)
				}
				return
			}
			if !res.Ambiguous {
				t.Fatalf("expected ambiguity %q, got none", tc.want)
			}
			if !reflect.DeepEqual(res.String, tc.want) {
				t.Fatalf("got %q, want %q", res.String, tc.want)
			}
		})
	}
}

func TestBudgetExhaustion(t *testing.T) {
	gr := g("a +", "E", "E", r("E", "E + E"), r("E", "a"))
	res, err := Check(gr, Options{MaxLength: 9, Budget: 3})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.BudgetExceeded {
		t.Fatalf("expected budget exhaustion, got %+v", res)
	}
	if res.Ambiguous {
		t.Fatalf("budget-exhausted run must not claim a result")
	}
}

func TestValidation(t *testing.T) {
	bad := []*Grammar{
		g("a", "S", "S", Rule{LHS: "S", RHS: nil}),              // epsilon
		g("a", "S", "S", r("S", "x")),                           // undeclared symbol
		g("a", "S", "Q", r("S", "a")),                           // bad start
		g("a S", "S", "S", r("S", "a")),                         // overlapping alphabets
		{Terminals: []string{"a"}, NonTerminals: []string{"S"}}, // no rules
	}
	for i, gr := range bad {
		if _, err := Check(gr, Options{MaxLength: 3}); err == nil {
			t.Fatalf("grammar %d: expected validation error", i)
		}
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	gr := g("a", "S", "S", r("S", "S S"), r("S", "a"))
	res := check(t, gr, 3)
	if !res.Ambiguous {
		t.Fatal("expected ambiguity")
	}
	// Two identical trees must be rejected.
	forged := &Result{Ambiguous: true, String: res.String, Tree1: res.Tree1, Tree2: res.Tree1}
	if err := forged.Verify(gr); err == nil {
		t.Fatal("Verify accepted identical trees")
	}
	// A tree for the wrong string must be rejected.
	other := check(t, g("a", "S", "S", r("S", "S S"), r("S", "a")), 3)
	forged = &Result{Ambiguous: true, String: []string{"a"}, Tree1: other.Tree1, Tree2: other.Tree2}
	if err := forged.Verify(gr); err == nil {
		t.Fatal("Verify accepted trees for the wrong string")
	}
}
