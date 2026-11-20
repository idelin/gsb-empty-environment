package bootpolicy

import (
	"strings"
	"testing"
	"time"
)

func TestCompressEighteenCandidates(t *testing.T) {
	longPrefix := "vendor/firmware/" + strings.Repeat("component-", 40)
	entries := make([]Entry, 0, 9)
	candidates := make([]Entry, 0, 18)

	// Nine original deny rules covering distinct 32-bit generation windows and
	// long, nested labels.
	for i := 0; i < 9; i++ {
		lo := uint32(i) * (maxUint32 / 9)
		hi := uint32(i+1)*(maxUint32/9) - 1
		if i == 8 {
			hi = maxUint32
		}
		e := Entry{
			Kind: KindDeny, SignerID: "permanent-signer-id",
			LabelPrefix: longPrefix, GenLo: lo, GenHi: hi,
		}
		entries = append(entries, e)
		candidates = append(candidates, e)
	}

	// Nine extra candidates: partial ranges, overlapping windows, a parent
	// scoped deny, an exact original exemption and varied locations.
	exempt := Entry{
		Kind: KindExempt, SignerID: "permanent-signer-id", LabelPrefix: longPrefix,
		GenLo: 0, GenHi: 10, Digest: digestOf(0x7E), NotAfter: 1 << 40,
		RootID: "root-of-trust",
	}
	entries = append(entries, exempt)
	candidates = append(candidates,
		exempt,
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: longPrefix,
			GenLo: 0, GenHi: maxUint32 / 2},
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: longPrefix,
			GenLo: maxUint32/2 + 1, GenHi: maxUint32},
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: longPrefix,
			GenLo: 0, GenHi: maxUint32, Location: "spi-nor"},
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: longPrefix,
			GenLo: 0, GenHi: maxUint32,
			Parent: &ParentScope{SignerID: "permanent-signer-id", Label: "root-bl"}},
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: longPrefix + "/x",
			GenLo: 0, GenHi: maxUint32},
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: longPrefix + "/y",
			GenLo: 0, GenHi: maxUint32},
		Entry{Kind: KindDeny, SignerID: "other-signer", LabelPrefix: longPrefix,
			GenLo: 0, GenHi: maxUint32},
		Entry{Kind: KindDeny, SignerID: "permanent-signer-id", LabelPrefix: "unrelated",
			GenLo: 0, GenHi: maxUint32},
	)

	if len(candidates) != 18 {
		t.Fatalf("candidate count = %d, want 18", len(candidates))
	}
	original := &Policy{Entries: entries}

	start := time.Now()
	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("18-candidate compression selected %d entries in %s", len(res.SelectedIndexes), time.Since(start))
	if time.Since(start) > 5*time.Second {
		t.Fatalf("compression took %s", time.Since(start))
	}

	compressed := &Policy{Entries: res.Selected}
	if err := VerifyCertificate(original, compressed, res.Certificate); err != nil {
		t.Fatal(err)
	}

	blob, err := EncodePackage(compressed, res.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportPackage(blob, original); err != nil {
		t.Fatal(err)
	}
}
