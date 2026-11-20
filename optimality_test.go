package bootpolicy

import "testing"

// TestOptimalityBruteForce exhaustively checks every subset of an 8-candidate
// catalog and proves Compress returns a minimum encoded-size equivalent set.
// The 18-candidate stress test focuses on scale; the search algorithm is the
// same exhaustive DFS in both cases.
func TestOptimalityBruteForce(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "a", GenLo: 0, GenHi: 100},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "a", GenLo: 101, GenHi: 200},
		{Kind: KindDeny, SignerID: "s2", LabelPrefix: "b", GenLo: 0, GenHi: 50,
			Parent: &ParentScope{SignerID: "s1", Label: "a"}},
		{
			Kind: KindExempt, SignerID: "s1", LabelPrefix: "a", GenLo: 0, GenHi: 100,
			Digest: digestOf(5), NotAfter: 7,
		},
	}}
	candidates := []Entry{
		original.Entries[0],
		original.Entries[1],
		original.Entries[2],
		original.Entries[3],
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "a", GenLo: 0, GenHi: 200},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "a", GenLo: 0, GenHi: 150},
		{Kind: KindDeny, SignerID: "s2", LabelPrefix: "b", GenLo: 0, GenHi: 50},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "a", GenLo: 50, GenHi: 200},
	}
	if len(candidates) != 8 {
		t.Fatalf("candidate count = %d", len(candidates))
	}

	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}

	equivalentCount := 0
	for mask := 0; mask < 1<<len(candidates); mask++ {
		sub := subsetEntries(candidates, mask)
		if !policiesEquivalent(original, &Policy{Entries: sub}) {
			continue
		}
		equivalentCount++
		if entriesWireSize(sub) < res.WireSize {
			t.Fatalf("subset %b cost %d beats selected %d", mask, entriesWireSize(sub), res.WireSize)
		}
	}
	if equivalentCount == 0 {
		t.Fatal("brute force found no equivalent subset, but Compress did")
	}

	if err := VerifyCertificate(original, &Policy{Entries: res.Selected}, res.Certificate); err != nil {
		t.Fatal(err)
	}
}
