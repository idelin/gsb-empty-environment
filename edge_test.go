package bootpolicy

import "testing"

func TestCompressEmptyAndExemptOnly(t *testing.T) {
	// Original policy with no entries admits everything; empty candidate set
	// is the unique minimum equivalent subset.
	empty := &Policy{}
	res, err := Compress(empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Selected) != 0 || res.WireSize != 0 {
		t.Fatalf("empty policy result = %+v", res)
	}
	if err := VerifyCertificate(empty, &Policy{Entries: res.Selected}, res.Certificate); err != nil {
		t.Fatal(err)
	}

	// Exempt-only original policy: nothing is denied, so dropping every entry
	// preserves every admission decision.
	exemptOnly := &Policy{Entries: []Entry{{
		Kind: KindExempt, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: maxUint32,
		Digest: digestOf(1),
	}}}
	res2, err := Compress(exemptOnly, exemptOnly.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Selected) != 0 {
		t.Fatalf("exempt-only policy should compress to empty, got %d entries", len(res2.Selected))
	}
}

func TestCompressDeterministic(t *testing.T) {
	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100},
	}}
	candidates := []Entry{
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100},
		{Kind: KindDeny, SignerID: "s", LabelPrefix: "c", GenLo: 0, GenHi: 100},
	}
	first, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !intsEqual(first.SelectedIndexes, second.SelectedIndexes) {
		t.Fatalf("non-deterministic selection: %v vs %v", first.SelectedIndexes, second.SelectedIndexes)
	}
}

func TestTooManyCandidates(t *testing.T) {
	original := &Policy{}
	candidates := make([]Entry, MaxCandidates+1)
	for i := range candidates {
		candidates[i] = Entry{Kind: KindDeny, SignerID: "s", GenLo: 0, GenHi: 1}
	}
	if _, err := Compress(original, candidates); err == nil {
		t.Fatal("expected error for over-limit candidate set")
	}
}

func TestInvalidEntryRejected(t *testing.T) {
	bad := []Entry{
		{Kind: KindDeny, SignerID: "s", GenLo: 10, GenHi: 5},
		{Kind: KindExempt, SignerID: "s", GenLo: 0, GenHi: 1},
		{Kind: EntryKind(9), SignerID: "s", GenLo: 0, GenHi: 1},
		{Kind: KindDeny, GenLo: 0, GenHi: 1},
	}
	for _, e := range bad {
		if err := e.Validate(); err == nil {
			t.Fatalf("entry expected to be invalid: %+v", e)
		}
	}
}
