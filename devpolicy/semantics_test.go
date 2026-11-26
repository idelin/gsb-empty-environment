package devpolicy

import (
	"math/rand"
	"testing"
)

func TestRandomPoliciesMatchBruteForce(t *testing.T) {
	const domainMax = uint16(7)
	for seed := int64(0); seed < 80; seed++ {
		rng := rand.New(rand.NewSource(seed))
		rules := randomPolicy(rng, 3, 14, domainMax)
		exhaustivelyCompare(t, rules, domainMax)
	}
}

func TestSingleLayerAccumulatorSemantics(t *testing.T) {
	const domainMax = uint16(5)
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: domainMax, MinorMin: 0, MinorMax: domainMax, Perm: BitR | BitW, Name: "open-rw"},
		{Layer: 0, Order: 1, Effect: EffectDeny, Type: TypeBlock, MajorMin: 2, MajorMax: 3, MinorMin: 0, MinorMax: domainMax, Perm: BitW, Name: "strip-w"},
		{Layer: 0, Order: 2, Effect: EffectAllow, Type: TypeBlock, MajorMin: 2, MajorMax: 2, MinorMin: 1, MinorMax: 1, Perm: BitM, Name: "add-m"},
	}
	compiled := exhaustivelyCompare(t, rules, domainMax)
	mask := compiled.EffectiveMask(TypeBlock, 2, 1)
	if mask != BitR|BitM {
		t.Fatalf("m-only isolation broken: mask=0b%03b want r|m", mask)
	}
}

func TestHierarchySuppression(t *testing.T) {
	const domainMax = uint16(6)
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeChar, MajorMin: 1, MajorMax: 4, MinorMin: 0, MinorMax: domainMax, Perm: MaskAll, Name: "root-grant"},
		{Layer: 1, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: domainMax, MinorMin: 0, MinorMax: domainMax, Perm: MaskAll, Name: "child-overreach"},
		{Layer: 0, Order: 1, Effect: EffectDeny, Type: TypeChar, MajorMin: 2, MajorMax: 2, MinorMin: 0, MinorMax: domainMax, Perm: BitW | BitM, Name: "root-deny"},
	}
	compiled := exhaustivelyCompare(t, rules, domainMax)
	if got := compiled.EffectiveMask(TypeBlock, 0, 0); got != 0 {
		t.Fatalf("child resurrected block access: 0b%03b", got)
	}
	if got := compiled.EffectiveMask(TypeChar, 2, 3); got != BitR {
		t.Fatalf("ancestor explicit deny restored: 0b%03b want r only", got)
	}
	if got := compiled.EffectiveMask(TypeChar, 5, 0); got != 0 {
		t.Fatalf("ungranted region leaked: 0b%03b", got)
	}

	spec := &Spec{}
	// The spec grants read only across the root window and rwm solely on major 1.
	// Hence the policy's w/m grants on majors 3..4 are escalations, whose
	// lexicographic minimum is (c,3,0,w).
	spec.AddAllow(TypeChar, 1, 4, 0, domainMax, BitR, "spec-r")
	spec.AddAllow(TypeChar, 1, 1, 0, domainMax, BitW|BitM, "spec-major1")
	specCompiled, err := spec.CompileDomain(domainMax)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Audit(compiled, specCompiled)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent || report.MinEscalation == nil {
		t.Fatalf("expected escalation, report=%+v", report)
	}
	v := report.MinEscalation
	if v.Type != TypeChar || v.Major != 3 || v.Minor != 0 || v.Bit != BitW {
		t.Fatalf("unexpected min escalation: %c (%d,%d) bit %s",
			v.Type, v.Major, v.Minor, BitName(v.Bit))
	}
	if len(v.PolicyWitness.MatchedRules()) == 0 {
		t.Fatal("escalation witness has no matched rules")
	}
}

func TestMknodNeverImpliesReadWrite(t *testing.T) {
	const domainMax = uint16(3)
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: domainMax, MinorMin: 0, MinorMax: domainMax, Perm: BitM, Name: "mknod-only"},
	}
	compiled := exhaustivelyCompare(t, rules, domainMax)
	for _, typ := range orderedTypes() {
		for major := uint16(0); major <= domainMax; major++ {
			for minor := uint16(0); minor <= domainMax; minor++ {
				if got := compiled.EffectiveMask(typ, major, minor); got != BitM {
					t.Fatalf("type %c (%d,%d): m implied extra bits 0b%03b", typ, major, minor, got)
				}
			}
		}
	}
}

func TestCrossIntervalSplitting(t *testing.T) {
	const domainMax = uint16(9)
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeChar, MajorMin: 0, MajorMax: domainMax, MinorMin: 0, MinorMax: domainMax, Perm: BitR, Name: "base-r"},
		{Layer: 0, Order: 1, Effect: EffectAllow, Type: TypeChar, MajorMin: 3, MajorMax: 7, MinorMin: 2, MinorMax: 8, Perm: BitW, Name: "cross-w"},
		{Layer: 0, Order: 2, Effect: EffectDeny, Type: TypeChar, MajorMin: 5, MajorMax: domainMax, MinorMin: 4, MinorMax: 6, Perm: BitR, Name: "punch-r"},
		{Layer: 0, Order: 3, Effect: EffectAllow, Type: TypeChar, MajorMin: 0, MajorMax: 4, MinorMin: 5, MinorMax: domainMax, Perm: BitM, Name: "thin-m"},
	}
	compiled := exhaustivelyCompare(t, rules, domainMax)
	if cells := len(compiled.Effective[typeIndex(TypeChar)].Partition); cells >= int(domainMax+1)*int(domainMax+1) {
		t.Fatalf("diagram degenerated to point enumeration: %d cells", cells)
	}
}
