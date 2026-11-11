package netsec

import (
	"math/rand"
	"testing"
)

func randomUniverse(t *testing.T, rng *rand.Rand) *Universe {
	t.Helper()
	values := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = prefix + "-" + string(rune('a'+i))
		}
		return out
	}
	tagCount := rng.Intn(2)
	tags := make([]TagDomain, tagCount)
	for i := range tags {
		tags[i] = TagDomain{
			Key:    "tag" + string(rune('a'+i)),
			Values: values("v", 1+rng.Intn(3)),
		}
	}
	u, err := NewUniverse(
		values("p", 1+rng.Intn(3)),
		values("a", 1+rng.Intn(3)),
		values("r", 1+rng.Intn(3)),
		tags...,
	)
	if err != nil {
		t.Fatalf("random universe: %v", err)
	}
	return u
}

func randomCondition(rng *rand.Rand, u *Universe) Condition {
	dim := rng.Intn(u.Dims())
	domain := u.Values(dim)
	count := 1 + rng.Intn(len(domain))
	rng.Shuffle(len(domain), func(i, j int) { domain[i], domain[j] = domain[j], domain[i] })
	atom := In{Axis: dim, Values: domain[:count]}

	switch rng.Intn(4) {
	case 0:
		return atom
	case 1:
		return Not{Inner: atom}
	case 2:
		return And{All: []Condition{atom, randomCondition(rng, u)}}
	default:
		return Or{Any: []Condition{atom, randomCondition(rng, u)}}
	}
}

func randomPolicy(t *testing.T, rng *rand.Rand, u *Universe, prefix string) Policy {
	t.Helper()
	rules := make([]Rule, rng.Intn(5))
	for i := range rules {
		effect := Allow
		if rng.Intn(2) == 0 {
			effect = Deny
		}
		rules[i] = Rule{
			ID:        prefix + "-rule-" + string(rune('a'+i)),
			Effect:    effect,
			Condition: randomCondition(rng, u),
		}
	}
	return Policy{Name: prefix, Rules: rules}
}

func TestRandomPolicyProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))

	for iteration := 0; iteration < 200; iteration++ {
		u := randomUniverse(t, rng)
		oldPolicy := randomPolicy(t, rng, u, "old")
		newPolicy := randomPolicy(t, rng, u, "new")

		pager, err := Diff(u, oldPolicy, newPolicy, 1+rng.Intn(3))
		if err != nil {
			t.Fatalf("iteration %d Diff: %v", iteration, err)
		}

		var findings []Finding
		var remainder []Rect
		pages := 0
		for {
			report := pager.Next()
			pages++
			findings = append(findings, report.Findings...)
			remainder = report.Remainder

			// Every non-final page must return its declared remainder.
			if !report.Complete && len(report.Remainder) == 0 {
				t.Fatalf("iteration %d: incomplete page without remainder", iteration)
			}
			if report.Complete {
				break
			}
			if pages > u.Size()+2 {
				t.Fatalf("iteration %d: pager never terminates", iteration)
			}
		}

		verification, err := Verify(u, oldPolicy, newPolicy, findings, remainder)
		if err != nil {
			t.Fatalf("iteration %d Verify: %v", iteration, err)
		}
		if !verification.OK {
			t.Fatalf("iteration %d: verification = %#v", iteration, verification)
		}

		// Point-volume accounting: every finding volume is a real tuple count.
		volume := 0
		for _, finding := range findings {
			volume += finding.Points
		}
		if volume != verification.FindingsPoints {
			t.Fatalf("iteration %d: finding volume %d != covered %d", iteration, volume, verification.FindingsPoints)
		}
	}
}
