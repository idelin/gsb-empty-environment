package bootpolicy

import "testing"

func TestCompressPicksMinimumBytes(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100},
	}}
	narrow := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 50}
	wide := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100}
	redundant := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "cc", GenLo: 0, GenHi: 100}
	candidates := []Entry{narrow, wide, redundant}

	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !intsEqual(res.SelectedIndexes, []int{1}) {
		t.Fatalf("selected %v, want [1]", res.SelectedIndexes)
	}

	for mask := 0; mask < 1<<len(candidates); mask++ {
		sub := subsetEntries(candidates, mask)
		cost := entriesWireSize(sub)
		if policiesEquivalent(original, &Policy{Entries: sub}) && cost < res.WireSize {
			t.Fatalf("subset %b cost %d beats selected cost %d", mask, cost, res.WireSize)
		}
	}
}

func TestCompressOverlappingRangesAndParents(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: 100,
			Parent: &ParentScope{SignerID: "s", Label: "bl-a"}},
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: 100,
			Parent: &ParentScope{SignerID: "s", Label: "bl-b"}},
	}}
	merged := Entry{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: 100}
	candidates := []Entry{
		original.Entries[0], original.Entries[1], merged,
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: 50},
	}
	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !intsEqual(res.SelectedIndexes, []int{0, 1}) {
		t.Fatalf("parent-scoped denies must not be merged into a broader deny, got %v", res.SelectedIndexes)
	}
	if err := VerifyCertificate(original, &Policy{Entries: res.Selected}, res.Certificate); err != nil {
		t.Fatal(err)
	}

	// A genuinely overlapping merge is legal when scopes agree.
	overlapOriginal := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: 60},
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 61, GenHi: 100},
	}}
	overlapCandidates := []Entry{
		overlapOriginal.Entries[0], overlapOriginal.Entries[1],
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "lib", GenLo: 0, GenHi: 100},
	}
	overlapRes, err := Compress(overlapOriginal, overlapCandidates)
	if err != nil {
		t.Fatal(err)
	}
	if !intsEqual(overlapRes.SelectedIndexes, []int{2}) {
		t.Fatalf("expected single merged range, got %v", overlapRes.SelectedIndexes)
	}
}

func TestCompressRejectsNewExemption(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: maxUint32},
	}}
	newExempt := Entry{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: maxUint32,
		Digest: digestOf(1),
	}
	if _, err := Compress(original, []Entry{newExempt}); err != ErrNewExemption {
		t.Fatalf("error = %v, want ErrNewExemption", err)
	}

	exempt := Entry{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: maxUint32,
		Digest: digestOf(1), NotAfter: 42,
	}
	origWithExempt := &Policy{Entries: []Entry{original.Entries[0], exempt}}
	candidates := []Entry{original.Entries[0], exempt}
	res, err := Compress(origWithExempt, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(origWithExempt, &Policy{Entries: res.Selected}, res.Certificate); err != nil {
		t.Fatal(err)
	}
}

func TestNoEquivalentSet(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 10},
	}}
	candidates := []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 5},
	}
	if _, err := Compress(original, candidates); err != ErrNoEquivalentSet {
		t.Fatalf("error = %v, want ErrNoEquivalentSet", err)
	}
}

func TestPackageRoundTrip(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "vendor", LabelPrefix: "boot", GenLo: 1, GenHi: 9, Location: "spi"},
		{
			Kind: KindExempt, SignerID: "vendor", LabelPrefix: "boot", GenLo: 1, GenHi: 9,
			RootID: "root", Digest: digestOf(0x42), NotAfter: 999,
			Parent: &ParentScope{SignerID: "vendor", Label: "bl"},
		},
	}}
	candidates := []Entry{original.Entries[0], original.Entries[1]}
	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := EncodePackage(&Policy{Entries: res.Selected}, res.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	imported, _, err := ImportPackage(blob, original)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Entries) != len(res.Selected) {
		t.Fatal("imported entry count mismatch")
	}

	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)/2] ^= 0xFF
	if _, _, err := ImportPackage(tampered, original); err == nil {
		t.Fatal("corrupt package imported")
	}
}

func subsetEntries(candidates []Entry, mask int) []Entry {
	out := make([]Entry, 0)
	for i := range candidates {
		if mask&(1<<i) != 0 {
			out = append(out, candidates[i])
		}
	}
	return out
}

func entriesWireSize(entries []Entry) int {
	total := 0
	for i := range entries {
		total += EntryWireSize(entries[i])
	}
	return total
}

func policiesEquivalent(a, b *Policy) bool {
	union := append(append([]Entry(nil), a.Entries...), b.Entries...)
	part, err := BuildPartition(union, len(a.Entries), 0)
	if err != nil {
		return false
	}
	for _, g := range part.groups {
		if a.EventAdmit(g.event) != b.EventAdmit(g.event) {
			return false
		}
	}
	return true
}
