package evidence_test

import (
	"testing"

	"example.org/domainreissue/q012/evidence"
)

func TestStrongKleeneOperators(t *testing.T) {
	values := []evidence.Value{evidence.Unknown, evidence.True, evidence.False}

	wantNot := map[evidence.Value]evidence.Value{
		evidence.Unknown: evidence.Unknown,
		evidence.True:    evidence.False,
		evidence.False:   evidence.True,
	}
	for _, v := range values {
		if got := evidence.Not(v); got != wantNot[v] {
			t.Fatalf("Not(%s)=%s, want %s", v, got, wantNot[v])
		}
	}

	for _, left := range values {
		for _, right := range values {
			gotAnd := evidence.And(left, right)
			gotOr := evidence.Or(left, right)

			var wantAnd evidence.Value
			switch {
			case left == evidence.False || right == evidence.False:
				wantAnd = evidence.False
			case left == evidence.Unknown || right == evidence.Unknown:
				wantAnd = evidence.Unknown
			default:
				wantAnd = evidence.True
			}
			var wantOr evidence.Value
			switch {
			case left == evidence.True || right == evidence.True:
				wantOr = evidence.True
			case left == evidence.Unknown || right == evidence.Unknown:
				wantOr = evidence.Unknown
			default:
				wantOr = evidence.False
			}
			if gotAnd != wantAnd {
				t.Fatalf("And(%s,%s)=%s, want %s", left, right, gotAnd, wantAnd)
			}
			if gotOr != wantOr {
				t.Fatalf("Or(%s,%s)=%s, want %s", left, right, gotOr, wantOr)
			}
		}
	}
}

func TestUnknownCannotActAsFalseForNegation(t *testing.T) {
	message := []byte("message under evaluation")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)

	// "no disavowed SPF result" would release trivially if missing evidence
	// were treated as False. Under strong Kleene it stays Unknown.
	rule := parseRule(t, "not spf.disavowed")

	noEvidence, err := engine.Evaluate(rule, message, nil)
	if err != nil {
		t.Fatalf("Evaluate no evidence: %v", err)
	}
	if noEvidence.Release || noEvidence.Value != evidence.Unknown {
		t.Fatalf("not unknown must stay unknown, got %s", noEvidence.Value)
	}
	if len(noEvidence.MinimalSets) != 0 {
		t.Fatal("unknown rule must not produce releasing sets")
	}

	// Only an authenticated False makes the negation True.
	disavowed := mustSign(t, relay, "spf.disavowed", "spf", message, false)
	withFalse, err := engine.Evaluate(rule, message, []*evidence.Fact{disavowed})
	if err != nil {
		t.Fatalf("Evaluate with false fact: %v", err)
	}
	if !withFalse.Release || withFalse.Value != evidence.True {
		t.Fatalf("not false must be true, got %s", withFalse.Value)
	}
	if len(withFalse.MinimalSets) != 1 || len(withFalse.MinimalSets[0]) != 1 ||
		withFalse.MinimalSets[0][0].Name != "spf.disavowed" {
		t.Fatalf("minimal set must contain the authenticated false fact, got %+v",
			withFalse.MinimalSets)
	}

	// An authenticated True makes the negation False.
	affirmed := mustSign(t, relay, "spf.disavowed", "spf", message, true)
	withTrue, err := engine.Evaluate(rule, message, []*evidence.Fact{affirmed})
	if err != nil {
		t.Fatalf("Evaluate with true fact: %v", err)
	}
	if withTrue.Release || withTrue.Value != evidence.False {
		t.Fatalf("not true must be false, got %s", withTrue.Value)
	}
}

func TestUnknownAsFalseIsStrictlyWeaker(t *testing.T) {
	message := []byte("gap demonstration")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)

	fact := mustSign(t, relay, "a", "method", message, true)
	rule := parseRule(t, "a and not b")

	decision, err := engine.Evaluate(rule, message, []*evidence.Fact{fact})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Release {
		t.Fatal("missing b must keep the conjunction unknown")
	}

	naiveLookup := func(name string) evidence.Value {
		if name == "a" {
			return evidence.True
		}
		return evidence.False
	}
	if rule.Eval(naiveLookup) != evidence.True {
		t.Fatal("test premise: collapsing unknown to false would release")
	}
}
