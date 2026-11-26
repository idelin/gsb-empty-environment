package devpolicy

import "testing"

func TestOptimizerEquivalenceCatchesBug(t *testing.T) {
	const domainMax = uint16(8)
	original := &Policy{Rules: []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeBlock, MajorMin: 1, MajorMax: 6, MinorMin: 1, MinorMax: 6, Perm: BitR | BitW, Name: "orig-window"},
		{Layer: 0, Order: 1, Effect: EffectDeny, Type: TypeBlock, MajorMin: 3, MajorMax: 4, MinorMin: 3, MinorMax: 4, Perm: BitW, Name: "orig-deny"},
	}}
	buggy := &Policy{Rules: []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeBlock, MajorMin: 0, MajorMax: 7, MinorMin: 0, MinorMax: 7, Perm: BitR | BitW, Name: "buggy-wide"},
	}}
	ok, diff, err := EquivalentDomain(original, buggy, domainMax)
	if err != nil {
		t.Fatal(err)
	}
	if ok || diff == nil {
		t.Fatal("expected equivalence failure from optimizer bug")
	}
	if diff.Type != TypeBlock || diff.Major != 0 || diff.Minor != 0 || diff.Bit != BitR {
		t.Fatalf("unexpected first diff: %c (%d,%d) %s",
			diff.Type, diff.Major, diff.Minor, BitName(diff.Bit))
	}

	tiled := &Policy{Rules: []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeBlock, MajorMin: 1, MajorMax: 6, MinorMin: 1, MinorMax: 2, Perm: BitR | BitW, Name: "tile-a"},
		{Layer: 0, Order: 1, Effect: EffectAllow, Type: TypeBlock, MajorMin: 1, MajorMax: 6, MinorMin: 3, MinorMax: 6, Perm: BitR | BitW, Name: "tile-b"},
		{Layer: 0, Order: 2, Effect: EffectDeny, Type: TypeBlock, MajorMin: 3, MajorMax: 4, MinorMin: 3, MinorMax: 4, Perm: BitW, Name: "tile-deny"},
	}}
	ok, diff, err = EquivalentDomain(original, tiled, domainMax)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || diff != nil {
		t.Fatalf("valid optimizer rewrite rejected: %+v", diff)
	}
}

func TestAuditOverDenyMinimum(t *testing.T) {
	const domainMax = uint16(4)
	// Spec allows rwm on all char devices; policy grants only r, so the minimum
	// mis-denied tuple is (c,0,0,w).
	spec := &Spec{}
	spec.AddAllow(TypeChar, 0, domainMax, 0, domainMax, MaskAll, "spec-all")
	specCompiled, err := spec.CompileDomain(domainMax)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (&Policy{Rules: []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeChar, MajorMin: 0, MajorMax: domainMax, MinorMin: 0, MinorMax: domainMax, Perm: BitR, Name: "tight-r"},
	}}).CompileDomain(domainMax)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Audit(policy, specCompiled)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent || report.MinOverDeny == nil {
		t.Fatalf("expected over-deny, report=%+v", report)
	}
	v := report.MinOverDeny
	if v.Type != TypeChar || v.Major != 0 || v.Minor != 0 || v.Bit != BitW {
		t.Fatalf("unexpected min over-deny: %c (%d,%d) %s",
			v.Type, v.Major, v.Minor, BitName(v.Bit))
	}
	if report.MinEscalation != nil {
		t.Fatal("read-only policy cannot escalate over an rwm spec")
	}
}

func TestSpecMandatoryDenyWins(t *testing.T) {
	spec := &Spec{}
	spec.AddAllow(TypeBlock, 0, MaxDeviceNo, 0, MaxDeviceNo, MaskAll, "allow-all")
	spec.AddDeny(TypeBlock, 10, 10, 0, MaxDeviceNo, MaskAll, "host-device")
	compiled, err := spec.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.EffectiveMask(TypeBlock, 10, 99); got != 0 {
		t.Fatalf("mandatory deny overridden: 0b%03b", got)
	}
	if got := compiled.EffectiveMask(TypeBlock, 11, 0); got != MaskAll {
		t.Fatalf("allow region clipped incorrectly: 0b%03b", got)
	}
}

func TestDelegationDecay(t *testing.T) {
	granter, err := (&Policy{Rules: []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "granter"},
	}}).Compile()
	if err != nil {
		t.Fatal(err)
	}

	// Missing decay mask must be rejected.
	_, err = AuditDelegation(granter, &Delegation{
		Target: &Policy{Rules: []Rule{
			{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "delegate"},
		}},
	})
	if err == nil {
		t.Fatal("delegation without decay mask should fail validation")
	}

	decay := uint8(BitR | BitM) // w is stripped at the namespace boundary
	report, err := AuditDelegation(granter, &Delegation{
		DecayMask: &decay,
		Target: &Policy{Rules: []Rule{
			{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "delegate"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid {
		t.Fatalf("attenuated delegation reported escape: %+v", report.Escape)
	}
	if got := report.Decayed.EffectiveMask(TypeBlock, 4, 4); got != BitR|BitM {
		t.Fatalf("decay mask not applied: 0b%03b", got)
	}

	// A narrower granter combined with an ambitious delegate yields an escape
	// witness at the lexicographically smallest withheld tuple.
	strictGranter, err := (&Policy{Rules: []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeChar, MajorMin: 1, MajorMax: 2, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitR, Name: "strict"},
	}}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	fullDecay := uint8(MaskAll)
	escapeReport, err := AuditDelegation(strictGranter, &Delegation{
		DecayMask: &fullDecay,
		Target: &Policy{Rules: []Rule{
			{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "ambitious"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if escapeReport.Valid || escapeReport.Escape == nil {
		t.Fatal("expected namespace escape to be detected")
	}
	if escapeReport.Escape.Type != TypeChar || escapeReport.Escape.Major != 0 || escapeReport.Escape.Minor != 0 || escapeReport.Escape.Bit != BitR {
		t.Fatalf("unexpected escape tuple: %c (%d,%d) %s",
			escapeReport.Escape.Type, escapeReport.Escape.Major,
			escapeReport.Escape.Minor, BitName(escapeReport.Escape.Bit))
	}
}

func TestParseHelpers(t *testing.T) {
	perm, err := ParsePerm("m")
	if err != nil || perm != BitM {
		t.Fatalf("ParsePerm(m) = %b, %v", perm, err)
	}
	lo, hi, err := ParseInterval("*")
	if err != nil || lo != 0 || hi != MaxDeviceNo {
		t.Fatalf("ParseInterval(*) = %d..%d, %v", lo, hi, err)
	}
	lo, hi, err = ParseInterval("8..15")
	if err != nil || lo != 8 || hi != 15 {
		t.Fatalf("ParseInterval = %d..%d, %v", lo, hi, err)
	}
	rule, err := ParseRule("deny b 1 0..3 m")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Effect != EffectDeny || rule.Type != TypeBlock || rule.Perm != BitM ||
		rule.MajorMin != 1 || rule.MajorMax != 1 || rule.MinorMin != 0 || rule.MinorMax != 3 {
		t.Fatalf("parsed rule mismatch: %+v", rule)
	}
	if _, err := ParseRule("allow c * * rw"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePerm("x"); err == nil {
		t.Fatal("expected parse error for unknown permission")
	}
}

func TestValidationErrors(t *testing.T) {
	if _, err := (&Policy{Rules: []Rule{
		{Layer: 0, Effect: EffectAllow, Type: TypeChar, MajorMax: MaxDeviceNo + 1, MinorMax: 1, Perm: BitR},
	}}).Compile(); err == nil {
		t.Fatal("expected out-of-range rule error")
	}
	// A sparse layer (layer 1 with no layer 0 rule) is legal but default-deny,
	// so the deeper grant is fully suppressed.
	sparse, err := (&Policy{Rules: []Rule{
		{Layer: 1, Effect: EffectAllow, Type: TypeChar, MajorMax: 1, MinorMax: 1, Perm: BitR},
	}}).Compile()
	if err != nil {
		t.Fatalf("sparse layers should compile: %v", err)
	}
	if got := sparse.EffectiveMask(TypeChar, 0, 0); got != 0 {
		t.Fatalf("sparse layer escaped default-deny root: 0b%03b", got)
	}
	if _, err := (&Policy{Rules: []Rule{
		{Layer: 0, Effect: EffectAllow, Type: 'z', MajorMax: 1, MinorMax: 1, Perm: BitR},
	}}).Compile(); err == nil {
		t.Fatal("expected bad type error")
	}
}
