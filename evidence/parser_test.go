package evidence_test

import (
	"testing"

	"example.org/domainreissue/q012/evidence"
)

func TestParserPrecedenceAndRoundTrip(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"spf.pass", "spf.pass"},
		{"not spf.pass", "not spf.pass"},
		{"not not spf.pass", "not not spf.pass"},
		{"a and b or c", "(a and b) or c"},
		{"a or b and c", "a or (b and c)"},
		{"not a and b", "not a and b"},
		{"not (a and b)", "not (a and b)"},
		{"(a or b) and c", "(a or b) and c"},
		{"a.and-b:c_1", "a.and-b:c_1"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			rule, err := evidence.ParseRule(tc.input)
			if err != nil {
				t.Fatalf("ParseRule: %v", err)
			}
			if got := rule.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
			reparsed, err := evidence.ParseRule(rule.String())
			if err != nil {
				t.Fatalf("reparse %q: %v", rule.String(), err)
			}
			if reparsed.String() != rule.String() {
				t.Fatalf("round trip mismatch: %q vs %q", reparsed.String(), rule.String())
			}
		})
	}
}

func TestParserErrors(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"and",
		"a and",
		"not",
		"a or",
		"(a and b",
		"a and b)",
		"a $ b",
		"a b",
		"not and or",
	}
	for _, text := range invalid {
		if _, err := evidence.ParseRule(text); err == nil {
			t.Fatalf("ParseRule(%q) expected error", text)
		}
	}
}

func TestConstructorAndEvaluation(t *testing.T) {
	lookup := func(name string) evidence.Value {
		switch name {
		case "a":
			return evidence.True
		case "b":
			return evidence.Unknown
		default:
			return evidence.False
		}
	}

	rule, err := evidence.AndOf(evidence.AtomOf("a"), evidence.NotOf(evidence.AtomOf("b")))
	if err != nil {
		t.Fatalf("AndOf: %v", err)
	}
	if got := rule.Eval(lookup); got != evidence.Unknown {
		t.Fatalf("true and not unknown = %s, want unknown", got)
	}

	orRule, err := evidence.OrOf(evidence.AtomOf("a"), evidence.AtomOf("b"))
	if err != nil {
		t.Fatalf("OrOf: %v", err)
	}
	if got := orRule.Eval(lookup); got != evidence.True {
		t.Fatalf("true or unknown = %s, want true", got)
	}

	if _, err := evidence.AndOf(); err == nil {
		t.Fatal("empty AndOf must error")
	}
	if _, err := evidence.OrOf(nil); err == nil {
		t.Fatal("nil operand must error")
	}

	names := evidence.Names(rule)
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("Names = %v, want [a b]", names)
	}
}
