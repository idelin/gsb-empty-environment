package selftest

import (
	"math/big"
	"testing"

	. "prescreen.local/ps-89dba9148c"
)

func testTable() *TypeTable {
	return &TypeTable{
		Version:  "table-v7",
		RootType: "Transfer",
		Messages: map[string]MessageSpec{
			"Transfer": {
				Name: "Transfer",
				Fields: []FieldSpec{
					{Number: 1, Name: "payee", Kind: KindAddress, Role: RolePayee, Required: true},
					{Number: 2, Name: "asset", Kind: KindText, Role: RoleAsset, Required: true},
					{Number: 3, Name: "amount", Kind: KindInt, Role: RoleAmount, Required: true, Default: 0},
					{Number: 4, Name: "domain", Kind: KindText, Role: RoleDomain, Required: true},
					{Number: 5, Name: "deadline", Kind: KindInt, Role: RoleDeadline, Required: true},
					{Number: 9, Name: "memo", Kind: KindText, Role: RoleNonAuth, Optional: true, MaxItems: 1, Default: ""},
				},
			},
		},
	}
}

func coreMsg(payee, asset string, amount *big.Int) *Message {
	return &Message{
		Type: "Transfer",
		Fields: []Field{
			{Number: 1, Value: AddressValue(payee)},
			{Number: 2, Value: TextValue(asset)},
			{Number: 3, Value: IntValue(amount)},
			{Number: 4, Value: TextValue("hardware-wallet-sign")},
			{Number: 5, Value: IntValue(big.NewInt(1_800_000_000))},
		},
	}
}

func TestInvisibleAndHomoglyphDoNotCollapse(t *testing.T) {
	withZWJ := "ab​c"
	plain := "abc"
	safeZWJ, err := RenderRestrictedText(withZWJ)
	if err != nil {
		t.Fatal(err)
	}
	safePlain, err := RenderRestrictedText(plain)
	if err != nil {
		t.Fatal(err)
	}
	if safeZWJ == safePlain {
		t.Fatalf("zero-width text collapsed: %q == %q", safeZWJ, safePlain)
	}
	if got := VisualSkeleton(withZWJ); got != plain {
		t.Fatalf("visual skeleton should erase zero width for collision analysis: %q", got)
	}

	latin := "alice.eth"
	cyrillic := "аlice.eth" // first a is U+0430
	ok, err := SameSafeScreen(latin, cyrillic)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("safe renderer made Latin and Cyrillic spoof identical")
	}
	if VisualSkeleton(cyrillic) != VisualSkeleton(latin) {
		t.Fatal("test confusable pair should have same unsafe visual skeleton")
	}
}

func TestExplicitZeroVersusMissingDefault(t *testing.T) {
	table := testTable()
	zero := coreMsg("alice.eth", "ETH", big.NewInt(0))
	missing := coreMsg("alice.eth", "ETH", big.NewInt(0))
	missing.Fields = append(append([]Field{}, missing.Fields[:2]...), missing.Fields[3:]...)

	rule := &DisplayRule{OmitDefaults: true}
	rep := VerifySmallDomain(table, rule, []*Message{zero, missing})
	if rep.Safe {
		t.Fatalf("expected explicit zero/missing amount collision to be unsafe: %+v", rep)
	}
	if len(rep.Counterexamples) != 1 {
		t.Fatalf("expected one counterexample, got %d", len(rep.Counterexamples))
	}
	found := false
	for _, d := range rep.Counterexamples[0].Differences {
		if d.Path == "3" || d.Path == "3[0]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected amount path difference, got %+v", rep.Counterexamples[0].Differences)
	}
	if zero.ComputeRoot() == missing.ComputeRoot() {
		t.Fatal("explicit zero and missing field must have different message roots")
	}
}

func TestHostDeclaredNonAuthorizedFieldCanDiffer(t *testing.T) {
	table := testTable()
	a := coreMsg("alice.eth", "ETH", big.NewInt(100))
	a.Fields = append(a.Fields, Field{Number: 9, Value: TextValue("client-a")})
	b := coreMsg("alice.eth", "ETH", big.NewInt(100))
	b.Fields = append(b.Fields, Field{Number: 9, Value: TextValue("client-b")})

	rule := &DisplayRule{NonAuthorizedFields: []any{P(9)}}
	if rep := VerifySmallDomain(table, rule, []*Message{a, b}); !rep.Safe {
		t.Fatalf("declared non-authorized memo should be allowed: %+v", rep)
	}

	undeclared := &DisplayRule{}
	if rep := VerifySmallDomain(table, undeclared, []*Message{a, b}); rep.Safe {
		t.Fatal("undeclared memo difference should not be accepted")
	}
}

func TestUnsafeHomoglyphScreenIsDetected(t *testing.T) {
	table := testTable()
	a := coreMsg("alice.eth", "ETH", big.NewInt(1))
	b := coreMsg("аlice.eth", "ETH", big.NewInt(1))
	rule := &DisplayRule{TextPolicy: &TextPolicy{}}
	rep := VerifySmallDomain(table, rule, []*Message{a, b})
	if rep.Safe || len(rep.Counterexamples) == 0 {
		t.Fatalf("unsafe homoglyph rendering should produce a counterexample: %+v", rep)
	}
}

func TestRoundingIntervalAndUnknown(t *testing.T) {
	rule := &DisplayRule{
		Pages: []PageRule{{
			Title: "Amount",
			Fields: []FieldRule{{
				Path: P(3), Role: RoleAmount, Label: "amount",
				Rounding: &RoundingRule{Unit: big.NewInt(10), Mode: RoundHalfUp},
			}},
		}},
	}

	if got, err := RoundBigInt(big.NewInt(4), *rule.Pages[0].Fields[0].Rounding); err != nil || got.Int64() != 0 {
		t.Fatalf("4 should round down to 0, got %v err=%v", got, err)
	}
	if got, err := RoundBigInt(big.NewInt(5), *rule.Pages[0].Fields[0].Rounding); err != nil || got.Int64() != 10 {
		t.Fatalf("5 should half-up round to 10, got %v err=%v", got, err)
	}
	iv, err := RoundingInterval(big.NewInt(10), *rule.Pages[0].Fields[0].Rounding)
	if err != nil || iv.Lo.Int64() != 5 || iv.Hi.Int64() != 14 {
		t.Fatalf("unexpected half-up interval: %s err=%v", iv, err)
	}

	table := testTable()
	five := coreMsg("alice.eth", "ETH", big.NewInt(5))
	fourteen := coreMsg("alice.eth", "ETH", big.NewInt(14))
	if rep := VerifySmallDomain(table, rule, []*Message{five, fourteen}); rep.Safe {
		t.Fatal("rounded collision between exact amounts must be unsafe in exhaustive domain")
	}
	single := VerifySample(table, rule, []*Message{five})
	if !single.Unknown {
		t.Fatalf("non-exhaustive rounded rule should be Unknown, got %s", single.Verdict)
	}
}

func TestNear256BitAmountsUseBigInt(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	oneLess := new(big.Int).Sub(max, big.NewInt(1))
	if !Near256Bit(max) {
		t.Fatal("test amount is not near 256 bits")
	}
	table := testTable()
	a := coreMsg("alice.eth", "ETH", max)
	b := coreMsg("alice.eth", "ETH", oneLess)
	d, err := Render(table, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range d.Pages {
		for _, l := range p.Lines {
			if l.Amount != nil && l.Amount.Cmp(max) == 0 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("exact near-256-bit amount was not retained")
	}
	if rep := VerifySmallDomain(table, &DisplayRule{}, []*Message{a, b}); !rep.Safe {
		t.Fatalf("distinct exact amounts should have distinct screens: %+v", rep.Counterexamples)
	}
}

func outputsMsg(distribution ...*big.Int) *Message {
	var fields []Field
	for i, amt := range distribution {
		out := Field{
			Number: 6,
			Value: MessageValue(
				Field{Number: 10, Value: AddressValue("alice.eth")},
				Field{Number: 11, Value: TextValue("ETH")},
				Field{Number: 12, Value: IntValue(amt)},
			),
		}
		out.Ordinal = i
		fields = append(fields, out)
	}
	return &Message{Type: "Transfer", Fields: fields}
}

func outputTable() *TypeTable {
	tb := testTable()
	spec := tb.Messages["Transfer"]
	spec.Fields = append(spec.Fields, FieldSpec{
		Number: 6, Name: "outputs", Kind: KindMessage, Repeated: true, MaxItems: 8,
		Fields: []FieldSpec{
			{Number: 10, Name: "payee", Kind: KindAddress, Role: RolePayee},
			{Number: 11, Name: "asset", Kind: KindText, Role: RoleAsset},
			{Number: 12, Name: "amount", Kind: KindInt, Role: RoleAmount},
		},
	})
	tb.Messages["Transfer"] = spec
	return tb
}

func TestMergedAmountsEraseIdentityAndRequireDeclaration(t *testing.T) {
	table := outputTable()
	a := outputsMsg(big.NewInt(3), big.NewInt(7))
	b := outputsMsg(big.NewInt(1), big.NewInt(9))
	rule := &DisplayRule{
		MergeSameAsset: true,
		Pages:          []PageRule{{Title: "Merged"}},
	}
	d, err := Render(table, a, rule)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Pages[0].Lines) != 1 || !d.Pages[0].Lines[0].Merged {
		t.Fatalf("expected one merged line, got %+v", d.Pages[0].Lines)
	}
	if d.Pages[0].Lines[0].Amount.Int64() != 10 {
		t.Fatalf("merge total = %s", d.Pages[0].Lines[0].Amount)
	}
	rep := VerifySmallDomain(table, rule, []*Message{a, b})
	if rep.Safe {
		t.Fatal("redistributed merged amounts must remain unsafe without source declaration")
	}
	if len(rep.Merges) == 0 || rep.Merges[0].IdentityVisible {
		t.Fatalf("expected merge report with erased identity, got %+v", rep.Merges)
	}

	declared := &DisplayRule{
		MergeSameAsset: true,
		Pages:          []PageRule{{Title: "Merged"}},
		NonAuthorizedFields: []any{
			Path{{Field: 6, Index: -1}, {Field: 12, Index: -1}},
		},
	}
	if rep := VerifySmallDomain(table, declared, []*Message{a, b}); !rep.Safe {
		t.Fatalf("declared source amount redistribution should be safe for aggregate screen: %+v", rep)
	}
}

func TestPageProofAndCrossMessageReplayAfterPowerCycle(t *testing.T) {
	table := testTable()
	a := coreMsg("alice.eth", "ETH", big.NewInt(42))
	a.Fields = append(a.Fields, Field{Number: 9, Value: TextValue("same-preview-a")})
	b := coreMsg("alice.eth", "ETH", big.NewInt(42))
	b.Fields = append(b.Fields, Field{Number: 9, Value: TextValue("same-preview-b")})

	rule := &DisplayRule{
		NonAuthorizedFields: []any{P(9)},
		Pages: []PageRule{
			{Title: "Preview"},
			{Title: "Details", PreviewPage: intPtr(0)},
		},
	}
	ra, err := NewSigningRequest(table, rule, a, uint64(77))
	if err != nil {
		t.Fatal(err)
	}
	rb, err := NewSigningRequest(table, rule, b, uint64(77))
	if err != nil {
		t.Fatal(err)
	}
	if ra.Display.VisualKey() != rb.Display.VisualKey() {
		t.Fatal("test requires same preview screens")
	}
	if ra.RequestHash == rb.RequestHash {
		t.Fatal("different raw message roots must produce different request hashes")
	}

	p0a, err := ra.Packet(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p0a.Verify(); !ok {
		t.Fatal("page 0 proof should verify")
	}
	p1a, err := ra.Packet(1)
	if err != nil {
		t.Fatal(err)
	}
	p1b, err := rb.Packet(1)
	if err != nil {
		t.Fatal(err)
	}

	dev := NewDevice([]byte("device-secret"))
	receipt, err := dev.IssueReceipt(p0a, ra.RequestHash)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := dev.PowerCycle()
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.AcceptReceipt(p1b, receipt); err == nil {
		t.Fatal("cross-message page receipt was accepted after power cycle")
	}
	if err := restarted.AcceptReceipt(p1a, receipt); err != nil {
		t.Fatalf("same-message next page should verify after power cycle: %v", err)
	}
	if err := restarted.AcceptReceipt(p1a, receipt); err == nil {
		t.Fatal("replayed page receipt was accepted")
	}
}

func intPtr(v int) *int { return &v }

func TestNestedListOrdinalsAndLeafProof(t *testing.T) {
	msg := &Message{Fields: []Field{{
		Number: 7,
		Value: ListValue(
			ListValue(IntValue(big.NewInt(1)), IntValue(big.NewInt(2))),
			ListValue(IntValue(big.NewInt(3))),
		),
	}}}
	leaves := msg.Leaves()
	want := []string{"7[0].7[0]", "7[0].7[1]", "7[1].7[0]"}
	if len(leaves) != len(want) {
		t.Fatalf("got %d leaves: %+v", len(leaves), leaves)
	}
	for i, key := range want {
		if leaves[i].Path.Key() != key {
			t.Fatalf("leaf %d path = %s, want %s", i, leaves[i].Path.Key(), key)
		}
	}
	rs, err := msg.Resolve(Path{{Field: 7, Index: 0}, {Field: 7, Index: 1}})
	if err != nil || len(rs) != 1 || rs[0].Value.Int.Int64() != 2 {
		t.Fatalf("nested resolve failed: %+v err=%v", rs, err)
	}
	proof, root, _, err := BuildLeafProof(msg, want[1])
	if err != nil {
		t.Fatal(err)
	}
	if !proof.Verify(root) {
		t.Fatal("leaf proof did not verify")
	}
}
