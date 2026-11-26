package devpolicy

import "testing"

func TestWildcardIntervalsAndTypeAllFanout(t *testing.T) {
	lo, hi, err := ParseInterval("*..*")
	if err != nil || lo != 0 || hi != MaxDeviceNo {
		t.Fatalf("wildcard interval = %d..%d, %v", lo, hi, err)
	}
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll,
			MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo,
			Perm: BitR | BitW | BitM, Name: "wildcard"},
	}
	compiled, err := (&Policy{Rules: rules}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range orderedTypes() {
		if got := compiled.EffectiveMask(typ, 0, 0); got != MaskAll {
			t.Fatalf("type %c wildcard not fanned out: 0b%03b", typ, got)
		}
		// A wildcard diagram collapses to a single full-plane cell.
		if cells := len(compiled.Effective[typeIndex(typ)].Partition); cells != 1 {
			t.Fatalf("type %c wildcard produced %d cells", typ, cells)
		}
	}
}

func TestWitnessRuleChain(t *testing.T) {
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeChar, MajorMin: 1, MajorMax: 3, MinorMin: 0, MinorMax: 9, Perm: BitR | BitW, Name: "grant"},
		{Layer: 0, Order: 1, Effect: EffectDeny, Type: TypeChar, MajorMin: 2, MajorMax: 2, MinorMin: 0, MinorMax: 9, Perm: BitW, Name: "strip"},
		{Layer: 1, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: 9, MinorMin: 0, MinorMax: 9, Perm: BitM, Name: "child"},
	}
	compiled, err := (&Policy{Rules: rules}).CompileDomain(9)
	if err != nil {
		t.Fatal(err)
	}
	w := compiled.Trace(TypeChar, 2, 4)
	if len(w.LayerMasks) != 2 || w.LayerMasks[0] != BitR || w.LayerMasks[1] != BitM {
		t.Fatalf("unexpected layer masks: %v", w.LayerMasks)
	}
	if w.Effective != 0 {
		t.Fatalf("nested effective mask should intersect to zero, got 0b%03b", w.Effective)
	}
	matched := w.MatchedRules()
	if len(matched) != 3 {
		t.Fatalf("expected 3 matched rules in witness, got %d", len(matched))
	}
	// The deny event must show the w bit being stripped.
	var sawStrip bool
	for _, ev := range matched {
		if ev.Rule.Name == "strip" && ev.Before == BitR|BitW && ev.After == BitR {
			sawStrip = true
		}
	}
	if !sawStrip {
		t.Fatal("witness did not record the deny transition")
	}
}

func TestTypeAndBitOrdering(t *testing.T) {
	if typeIndex(TypeChar) != 0 || typeIndex(TypeBlock) != 1 {
		t.Fatal("char must sort before block")
	}
	if bitRank(BitR) != 0 || bitRank(BitW) != 1 || bitRank(BitM) != 2 {
		t.Fatal("permission order must be r<w<m")
	}
	if got := BitName(BitM); got != "m" {
		t.Fatalf("BitName(m) = %q", got)
	}
}

func TestEmptyAndSparsePolicyDefaultDeny(t *testing.T) {
	empty, err := (&Policy{}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range orderedTypes() {
		if got := empty.EffectiveMask(typ, 0, 0); got != 0 {
			t.Fatalf("empty policy must default-deny, got 0b%03b", got)
		}
	}

	// A rule only at layer 2 still intersects two empty default-deny layers.
	sparse, err := (&Policy{Rules: []Rule{
		{Layer: 2, Effect: EffectAllow, Type: TypeBlock, MajorMax: 1, MinorMax: 1, Perm: MaskAll, Name: "deep"},
	}}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	if got := sparse.EffectiveMask(TypeBlock, 0, 0); got != 0 {
		t.Fatalf("sparse deep layer must stay denied, got 0b%03b", got)
	}
}

func TestRuleValidation(t *testing.T) {
	cases := []Rule{
		{Layer: 0, Effect: EffectAllow, Type: TypeChar, MajorMax: 1, MinorMax: 1, Perm: 0},
		{Layer: 0, Effect: EffectAllow, Type: TypeChar, MajorMin: 2, MajorMax: 1, MinorMax: 1, Perm: BitR},
		{Layer: 0, Effect: EffectAllow, Type: TypeChar, MajorMax: 1, MinorMax: 1, Perm: 7 << 1},
	}
	for i, r := range cases {
		if err := r.validate(); err == nil {
			t.Fatalf("case %d expected validation error", i)
		}
	}
}

func TestPublicBuilderAndErrorAPI(t *testing.T) {
	policy := (&Policy{}).
		Add(Rule{Layer: 0, Effect: EffectAllow, Type: TypeChar, MajorMax: 1, MinorMax: 1, Perm: BitR, Name: "a"}).
		Add(Rule{Layer: 0, Effect: EffectAllow, Type: TypeChar, MajorMax: 1, MinorMax: 1, Perm: BitM, Name: "b"})
	compiled, err := policy.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.EffectiveMask(TypeChar, 0, 0); got != BitR|BitM {
		t.Fatalf("builder Add broken: 0b%03b", got)
	}

	bad := &Policy{Rules: []Rule{
		{Layer: 0, Effect: EffectAllow, Type: 'z', MajorMax: 1, MinorMax: 1, Perm: BitR},
	}}
	if _, err := bad.Compile(); err == nil {
		t.Fatal("expected compile error")
	} else if pe, ok := AsPolicyError(err); !ok || pe.Error() == "" {
		t.Fatalf("error not exposed as PolicyError: %v", err)
	}
}

func TestEquivalentProductionEntry(t *testing.T) {
	original := &Policy{Rules: []Rule{
		{Layer: 0, Effect: EffectAllow, Type: TypeBlock, MajorMin: 1, MajorMax: 2, MinorMin: 1, MinorMax: 2, Perm: BitR, Name: "a"},
	}}
	same := &Policy{Rules: []Rule{
		{Layer: 0, Effect: EffectAllow, Type: TypeBlock, MajorMin: 1, MajorMax: 2, MinorMin: 1, MinorMax: 1, Perm: BitR, Name: "p1"},
		{Layer: 0, Effect: EffectAllow, Type: TypeBlock, MajorMin: 1, MajorMax: 2, MinorMin: 2, MinorMax: 2, Perm: BitR, Name: "p2"},
	}}
	ok, diff, err := Equivalent(original, same)
	if err != nil || !ok || diff != nil {
		t.Fatalf("equivalent rewrite rejected: ok=%v diff=%v err=%v", ok, diff, err)
	}
}

func TestRegionScopedDecay(t *testing.T) {
	granter, err := (&Policy{Rules: []Rule{
		{Layer: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "g"},
	}}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	global := uint8(MaskAll)
	report, err := AuditDelegation(granter, &Delegation{
		DecayMask: &global,
		RegionMask: []RegionDecay{
			{Rect: Rect{MajMin: 5, MajMax: 5, MinMin: 0, MinMax: MaxDeviceNo}, Mask: BitR},
		},
		Target: &Policy{Rules: []Rule{
			{Layer: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "t"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid {
		t.Fatalf("scoped decay falsely flagged escape: %+v", report.Escape)
	}
	if got := report.Decayed.EffectiveMask(TypeChar, 5, 10); got != BitR {
		t.Fatalf("region decay not applied: 0b%03b", got)
	}
	if got := report.Decayed.EffectiveMask(TypeChar, 6, 10); got != MaskAll {
		t.Fatalf("global decay altered region outside scope: 0b%03b", got)
	}

	badMask := uint8(MaskAll)
	if _, err := AuditDelegation(granter, &Delegation{
		DecayMask: &badMask,
		RegionMask: []RegionDecay{
			{Rect: Rect{MajMin: 1, MajMax: 0, MinMin: 0, MinMax: 1}, Mask: BitR},
		},
		Target: &Policy{},
	}); err == nil {
		t.Fatal("expected invalid region rectangle error")
	}
}
