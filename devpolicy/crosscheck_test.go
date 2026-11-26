package devpolicy

import (
	"math/rand"
	"testing"
)

// bruteViolation scans the full small domain point by point (test-only
// reference) and returns the lexicographically first tuple whose masks
// differ, together with the offending bit.
func bruteViolation(t *testing.T, want, got *CompiledPolicy, domainMax uint16) *Violation {
	t.Helper()
	for _, typ := range orderedTypes() {
		idx := typeIndex(typ)
		for major := uint16(0); major <= domainMax; major++ {
			for minor := uint16(0); minor <= domainMax; minor++ {
				expected := want.Effective[idx].maskAt(major, minor)
				actual := got.Effective[idx].maskAt(major, minor)
				if delta := actual ^ expected; delta != 0 {
					return &Violation{
						Type:     typ,
						Major:    major,
						Minor:    minor,
						Bit:      firstSetBit(delta),
						Expected: expected,
						Actual:   actual,
					}
				}
			}
		}
	}
	return nil
}

func TestEquivalenceMatchesBruteForceMinimum(t *testing.T) {
	const domainMax = uint16(6)
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed + 1000))
		base := randomPolicy(rng, 2, 8, domainMax)
		mutated := randomPolicy(rng, 2, 8, domainMax)

		a, err := (&Policy{Rules: base}).CompileDomain(domainMax)
		if err != nil {
			t.Fatal(err)
		}
		b, err := (&Policy{Rules: mutated}).CompileDomain(domainMax)
		if err != nil {
			t.Fatal(err)
		}
		ok, diff, err := EquivalentPolicies(a, b)
		if err != nil {
			t.Fatal(err)
		}
		ref := bruteViolation(t, a, b, domainMax)
		if ref == nil {
			if !ok || diff != nil {
				t.Fatalf("seed %d: symbolic reported diff but brute force found none", seed)
			}
			continue
		}
		if ok || diff == nil {
			t.Fatalf("seed %d: symbolic missed a brute-force difference", seed)
		}
		if diff.Type != ref.Type || diff.Major != ref.Major ||
			diff.Minor != ref.Minor || diff.Bit != ref.Bit {
			t.Fatalf("seed %d: min diff %c(%d,%d)%s != brute %c(%d,%d)%s",
				seed, diff.Type, diff.Major, diff.Minor, BitName(diff.Bit),
				ref.Type, ref.Major, ref.Minor, BitName(ref.Bit))
		}
	}
}

func TestAuditMatchesBruteForceMinimum(t *testing.T) {
	const domainMax = uint16(5)
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed + 2000))
		specRules := randomPolicy(rng, 1, 6, domainMax)
		policyRules := randomPolicy(rng, 2, 8, domainMax)

		spec, err := (&Policy{Rules: specRules}).CompileDomain(domainMax)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := (&Policy{Rules: policyRules}).CompileDomain(domainMax)
		if err != nil {
			t.Fatal(err)
		}
		report, err := Audit(policy, spec)
		if err != nil {
			t.Fatal(err)
		}

		var minEsc, minDeny *Violation
		for _, typ := range orderedTypes() {
			idx := typeIndex(typ)
			for major := uint16(0); major <= domainMax; major++ {
				for minor := uint16(0); minor <= domainMax; minor++ {
					expected := spec.Effective[idx].maskAt(major, minor)
					actual := policy.Effective[idx].maskAt(major, minor)
					record := func(bits uint8, escalation bool) *Violation {
						if bits == 0 {
							return nil
						}
						return &Violation{
							Escalation: escalation,
							Type:       typ,
							Major:      major,
							Minor:      minor,
							Bit:        firstSetBit(bits),
						}
					}
					if v := record(actual&^expected, true); v != nil && (minEsc == nil || v.less(minEsc)) {
						minEsc = v
					}
					if v := record(expected&^actual, false); v != nil && (minDeny == nil || v.less(minDeny)) {
						minDeny = v
					}
				}
			}
		}
		if (minEsc == nil) != (report.MinEscalation == nil) {
			t.Fatalf("seed %d: escalation presence mismatch", seed)
		}
		if minEsc != nil && !sameViolation(minEsc, report.MinEscalation) {
			t.Fatalf("seed %d: escalation min mismatch %+v vs %+v", seed, minEsc, report.MinEscalation)
		}
		if (minDeny == nil) != (report.MinOverDeny == nil) {
			t.Fatalf("seed %d: over-deny presence mismatch", seed)
		}
		if minDeny != nil && !sameViolation(minDeny, report.MinOverDeny) {
			t.Fatalf("seed %d: over-deny min mismatch %+v vs %+v", seed, minDeny, report.MinOverDeny)
		}
	}
}

func sameViolation(a, b *Violation) bool {
	return a.Type == b.Type && a.Major == b.Major && a.Minor == b.Minor && a.Bit == b.Bit
}
