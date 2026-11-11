package ldapfilter

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

var randomAttributes = []string{
	"cn", "sn", "mail", "uid", "member", "objectClass",
	"2.5.4.3", "cn;lang-en", "userCertificate;binary",
}

var randomRules = []string{
	"caseExactMatch", "caseIgnoreMatch", "2.5.13.5", "2.5.13.6",
}

// randomValues deliberately includes every RFC 4515 metacharacter, NUL,
// backslash escapes and multibyte UTF-8 so escaping is exercised hard.
var randomValues = []string{
	"",
	"alice",
	"*",
	"(",
	")",
	"\\",
	"\x00",
	"a*b",
	")(|(cn=*",
	"\\2a",
	"héllo",
	"日本語",
	"a\\b*c(d)e\x00f",
	"star*):(\\nul",
	"  spaced  ",
	"x\\2y",
}

func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xC0FFEE, 0xBADF00D))
	const iterations = 2000

	for iteration := 0; iteration < iterations; iteration++ {
		original := generateFilter(rng, 0)
		rendered, err := Build(original)
		if err != nil {
			t.Fatalf("iteration %d: Build error: %v\nfilter: %#v", iteration, err, original)
		}
		parsed, err := Parse(rendered)
		if err != nil {
			t.Fatalf("iteration %d: Parse error: %v\nrendered: %q", iteration, err, rendered)
		}
		if !reflect.DeepEqual(parsed, original) {
			t.Fatalf("iteration %d: round trip mismatch\nrendered: %q\ngot:  %#v\nwant: %#v",
				iteration, rendered, parsed, original)
		}

		// Re-parsing the canonical rendering must reach the same tree again.
		reparsed, err := Parse(parsed.String())
		if err != nil {
			t.Fatalf("iteration %d: second Parse error: %v", iteration, err)
		}
		if !reflect.DeepEqual(reparsed, original) {
			t.Fatalf("iteration %d: second parse mismatch", iteration)
		}
	}
}

func TestRandomRoundTripDifferentSeeds(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed*7919))
		for iteration := 0; iteration < 100; iteration++ {
			original := generateFilter(rng, 0)
			rendered, err := Build(original)
			if err != nil {
				t.Fatalf("seed %d: Build error: %v", seed, err)
			}
			parsed, err := Parse(rendered)
			if err != nil {
				t.Fatalf("seed %d: Parse(%q) error: %v", seed, rendered, err)
			}
			if !reflect.DeepEqual(parsed, original) {
				t.Fatalf("seed %d: round trip mismatch for %q", seed, rendered)
			}
		}
	}
}

func generateFilter(rng *rand.Rand, depth int) Filter {
	if depth < 4 && rng.IntN(10) < 4 {
		switch rng.IntN(3) {
		case 0:
			children := make([]Filter, 1+rng.IntN(3))
			for index := range children {
				children[index] = generateFilter(rng, depth+1)
			}
			return &AndFilter{Children: children}
		case 1:
			children := make([]Filter, 1+rng.IntN(3))
			for index := range children {
				children[index] = generateFilter(rng, depth+1)
			}
			return &OrFilter{Children: children}
		default:
			return &NotFilter{Child: generateFilter(rng, depth+1)}
		}
	}
	return generateLeaf(rng)
}

func generateLeaf(rng *rand.Rand) Filter {
	attribute := randomAttributes[rng.IntN(len(randomAttributes))]
	value := randomValues[rng.IntN(len(randomValues))]

	switch rng.IntN(9) {
	case 0:
		return &EqualityFilter{Attribute: attribute, Value: value}
	case 1:
		return &PresentFilter{Attribute: attribute}
	case 2:
		return &ApproxFilter{Attribute: attribute, Value: value}
	case 3:
		return &GreaterOrEqualFilter{Attribute: attribute, Value: value}
	case 4:
		return &LessOrEqualFilter{Attribute: attribute, Value: value}
	case 5:
		return generateSubstrings(rng, attribute)
	case 6, 7:
		return generateExtensible(rng, value)
	default:
		// Reuse a second equality with another random value for variety.
		return &EqualityFilter{Attribute: attribute, Value: randomValues[rng.IntN(len(randomValues))]}
	}
}

func generateSubstrings(rng *rand.Rand, attribute string) Filter {
	var any []string
	for index := rng.IntN(3); index > 0; index-- {
		any = append(any, randomValues[rng.IntN(len(randomValues))])
	}
	initial := randomValues[rng.IntN(len(randomValues))]
	final := randomValues[rng.IntN(len(randomValues))]
	if len(any) == 0 && initial == "" && final == "" {
		// The all-empty form renders attr=*, which is a presence assertion
		// rather than a substring assertion, so avoid that ambiguity.
		initial = randomValues[1]
	}
	return &SubstringsFilter{
		Attribute: attribute,
		Initial:   initial,
		Any:       any,
		Final:     final,
	}
}

func generateExtensible(rng *rand.Rand, value string) Filter {
	filter := &ExtensibleFilter{
		DNAttributes: rng.IntN(2) == 0,
		Value:        value,
	}
	switch rng.IntN(3) {
	case 0:
		filter.Attribute = randomAttributes[rng.IntN(len(randomAttributes))]
	case 1:
		filter.MatchingRule = randomRules[rng.IntN(len(randomRules))]
	default:
		filter.Attribute = randomAttributes[rng.IntN(len(randomAttributes))]
		filter.MatchingRule = randomRules[rng.IntN(len(randomRules))]
	}
	return filter
}
