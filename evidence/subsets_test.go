package evidence_test

import (
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"example.org/domainreissue/q012/evidence"
)

func factSetNames(sets [][]*evidence.Fact) [][]string {
	out := make([][]string, len(sets))
	for i, set := range sets {
		names := make([]string, len(set))
		for j, fact := range set {
			names[j] = fact.Name
		}
		sort.Strings(names)
		out[i] = names
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]) < fmt.Sprint(out[j])
	})
	return out
}

func masksFromSets(sets [][]*evidence.Fact, names []string) []uint {
	index := make(map[string]uint, len(names))
	for i, name := range names {
		index[name] = uint(1) << uint(i)
	}
	masks := make([]uint, 0, len(sets))
	for _, set := range sets {
		var mask uint
		for _, fact := range set {
			mask |= index[fact.Name]
		}
		masks = append(masks, mask)
	}
	sortUint(masks)
	return masks
}

func sortUint(values []uint) {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
}

func TestMinimalReleasingSets(t *testing.T) {
	message := []byte("policy message")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)

	facts := []*evidence.Fact{
		mustSign(t, relay, "a", "m1", message, true),
		mustSign(t, relay, "b", "m2", message, true),
		mustSign(t, relay, "c", "m3", message, false),
		mustSign(t, relay, "d", "m4", message, true),
	}

	cases := []struct {
		rule     string
		setNames [][]string
	}{
		{"a", [][]string{{"a"}}},
		{"a and b", [][]string{{"a", "b"}}},
		{"a or d", [][]string{{"a"}, {"d"}}},
		{"not c", [][]string{{"c"}}},
		{"(a or d) and b", [][]string{{"a", "b"}, {"b", "d"}}},
		{"a and (not c)", [][]string{{"a", "c"}}},
	}

	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			rule := parseRule(t, tc.rule)
			decision, err := engine.Evaluate(rule, message, facts)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if !decision.Release {
				t.Fatalf("rule %q must release", tc.rule)
			}
			got := factSetNames(decision.MinimalSets)
			if !reflect.DeepEqual(got, tc.setNames) {
				t.Fatalf("rule %q minimal sets = %v, want %v", tc.rule, got, tc.setNames)
			}
			expectedCardinality := len(tc.setNames[0])
			for _, set := range decision.MinimalSets {
				if len(set) != expectedCardinality {
					t.Fatalf("set %v cardinality = %d, want %d", set, len(set), expectedCardinality)
				}
			}
		})
	}
}

func TestExhaustiveSubsetEnumeration(t *testing.T) {
	const n = 10
	message := []byte("enumeration message")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)

	rng := rand.New(rand.NewSource(42))
	facts := make([]*evidence.Fact, n)
	names := make([]string, n)
	bindings := make(map[string]evidence.Value, n)
	for i := range facts {
		name := fmt.Sprintf("f%d", i)
		names[i] = name
		claim := rng.Intn(2) == 1
		facts[i] = mustSign(t, relay, name, "method", message, claim)
		if claim {
			bindings[name] = evidence.True
		} else {
			bindings[name] = evidence.False
		}
	}

	rules := []string{
		"f0 and f1 and f2",
		"f0 or f1 or f2",
		"(f0 and f1) or (not f3 and f4)",
		"not f5",
		"(f0 or f1) and (f2 or not f3) and f6",
	}

	lookupForMask := func(mask uint) evidence.Lookup {
		return func(name string) evidence.Value {
			for bit := 0; bit < n; bit++ {
				if names[bit] == name && mask&(1<<bit) != 0 {
					return bindings[name]
				}
			}
			return evidence.Unknown
		}
	}

	for _, ruleText := range rules {
		t.Run(ruleText, func(t *testing.T) {
			rule := parseRule(t, ruleText)
			decision, err := engine.Evaluate(rule, message, facts)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			var bruteMasks []uint
			minPop := n + 1
			for mask := uint(1); mask < 1<<n; mask++ {
				if rule.Eval(lookupForMask(mask)) != evidence.True {
					continue
				}
				pop := bits.OnesCount(mask)
				if pop < minPop {
					minPop = pop
					bruteMasks = nil
				}
				if pop == minPop {
					bruteMasks = append(bruteMasks, mask)
				}
			}

			if len(bruteMasks) == 0 {
				if decision.Release {
					t.Fatalf("brute force found no release for %q", ruleText)
				}
				return
			}
			if !decision.Release {
				t.Fatalf("brute force found releases for %q", ruleText)
			}

			engineMasks := masksFromSets(decision.MinimalSets, names)
			sortUint(bruteMasks)
			if !reflect.DeepEqual(engineMasks, bruteMasks) {
				t.Fatalf("rule %q engine masks %b != brute masks %b",
					ruleText, engineMasks, bruteMasks)
			}
			for _, mask := range engineMasks {
				if bits.OnesCount(mask) != minPop {
					t.Fatalf("returned set is not minimum cardinality for %q", ruleText)
				}
				// Strong Kleene is monotone in defined evidence: adding more
				// authenticated facts cannot turn a True rule non-True.
				for sup := mask + 1; sup < 1<<n; sup++ {
					if sup&mask != mask {
						continue
					}
					if rule.Eval(lookupForMask(sup)) != evidence.True {
						t.Fatalf("superset %b of releasing set %b failed for %q",
							sup, mask, ruleText)
					}
				}
			}
		})
	}
}

func TestEighteenFactLimit(t *testing.T) {
	message := []byte("limit message")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)

	makeFacts := func(count int) []*evidence.Fact {
		facts := make([]*evidence.Fact, count)
		for i := range facts {
			facts[i] = mustSign(t, relay, fmt.Sprintf("f%02d", i), "method", message, true)
		}
		return facts
	}

	t.Run("18 facts accepted", func(t *testing.T) {
		facts := makeFacts(evidence.DefaultMaxFacts)
		rule := parseRule(t, "f00 or f01 or f02 or f03 or f04 or f05 or f06 or f07 or f08")
		decision, err := engine.Evaluate(rule, message, facts)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if !decision.Release || len(decision.Authenticated) != evidence.DefaultMaxFacts {
			t.Fatalf("expected release over 18 facts, got %+v", decision)
		}
		if len(decision.MinimalSets) != 9 {
			t.Fatalf("expected nine single-fact minimal sets (one per OR operand), got %d",
				len(decision.MinimalSets))
		}
		for _, set := range decision.MinimalSets {
			if len(set) != 1 {
				t.Fatalf("each OR minimal set must contain one fact, got %d", len(set))
			}
		}

		all := parseRule(t,
			"f00 and f01 and f02 and f03 and f04 and f05 and f06 and f07 and f08 and "+
				"f09 and f10 and f11 and f12 and f13 and f14 and f15 and f16 and f17")
		full, err := engine.Evaluate(all, message, facts)
		if err != nil {
			t.Fatalf("Evaluate conjunction: %v", err)
		}
		if !full.Release || len(full.MinimalSets) != 1 || len(full.MinimalSets[0]) != 18 {
			t.Fatalf("expected the full 18-fact minimal set, got %+v", full.MinimalSets)
		}
	})

	t.Run("19th authenticated fact rejected", func(t *testing.T) {
		facts := makeFacts(evidence.DefaultMaxFacts + 1)
		rule := parseRule(t, "f00")
		_, err := engine.Evaluate(rule, message, facts)
		if err == nil {
			t.Fatal("expected MaxFacts error for 19 authenticated facts")
		}
	})

	t.Run("external facts do not count toward the limit", func(t *testing.T) {
		rogue := newTestNode(t, "rogue", false)
		trustedFacts := makeFacts(evidence.DefaultMaxFacts)
		external := make([]*evidence.Fact, 5)
		for i := range external {
			external[i] = mustSign(t, rogue, fmt.Sprintf("x%d", i), "method", message, true)
		}
		rule := parseRule(t, "f00")
		decision, err := engine.Evaluate(rule, message, append(trustedFacts, external...))
		if err != nil {
			t.Fatalf("external facts must not trip MaxFacts: %v", err)
		}
		if len(decision.Authenticated) != evidence.DefaultMaxFacts {
			t.Fatalf("expected 18 authenticated facts, got %d", len(decision.Authenticated))
		}
		for _, name := range []string{"x0", "x1", "x2", "x3", "x4"} {
			if decision.Values[name] != evidence.Unknown {
				t.Fatalf("external fact %s must be unknown", name)
			}
		}
	})
}
