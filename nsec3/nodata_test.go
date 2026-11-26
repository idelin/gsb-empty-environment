package nsec3

import (
	"errors"
	"testing"
)

func nodataRing(t *testing.T) []*Record {
	return buildRing(t, []ringRecord{
		// Apex with SOA+NS, used to exercise the DS/SOA apex rule.
		{"example.", []uint16{TypeSOA, TypeNS, TypeDNSKEY}, false},
		// Signed host with A but no AAAA.
		{"host.example.", []uint16{TypeA}, false},
		// Empty non-terminal anchor: "ent.example." exists only as an
		// ancestor label of "ent.child.example." and has no owner RR.
		{"ent.child.example.", []uint16{TypeA}, false},
	})
}

func TestVerifyNODATAExactMatch(t *testing.T) {
	v := newVerifier(t, nodataRing(t))
	proof, err := v.VerifyNODATA("host.example.", 28 /* AAAA */)
	if err != nil {
		t.Fatalf("VerifyNODATA: %v", err)
	}
	if proof.Kind != ProofNODATAExact || proof.Exact == nil {
		t.Fatalf("unexpected proof: %+v", proof)
	}
	if _, err := v.VerifyNODATA("host.example.", TypeA); !errors.Is(err, ErrTypePresent) {
		t.Fatalf("want ErrTypePresent, got %v", err)
	}
	// A CNAME bit contradicts any NODATA denial.
	cnameRing := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"alias.example.", []uint16{TypeCNAME}, false},
	})
	if _, err := newVerifier(t, cnameRing).VerifyNODATA("alias.example.", TypeA); !errors.Is(err, ErrCNAMEPresent) {
		t.Fatalf("want ErrCNAMEPresent, got %v", err)
	}
}

func TestVerifyNODATAEmptyNonTerminal(t *testing.T) {
	v := newVerifier(t, nodataRing(t))
	// ent.example has no owner record while ent.child.example does, so
	// the proof is a closest-encloser match plus next-closer coverage.
	proof, err := v.VerifyNODATA("ent.example.", TypeA)
	if err != nil {
		t.Fatalf("ENT NODATA: %v", err)
	}
	if proof.Kind != ProofNODATAENT {
		t.Fatalf("kind = %d, want ProofNODATAENT", proof.Kind)
	}
	if proof.ClosestEncloser != "example." {
		t.Fatalf("closest encloser = %q", proof.ClosestEncloser)
	}
}

func TestVerifyNODATADSAtApexRejectsSOA(t *testing.T) {
	v := newVerifier(t, nodataRing(t))
	_, err := v.VerifyNODATA("example.", TypeDS)
	if !errors.Is(err, ErrSOAPresent) {
		t.Fatalf("want ErrSOAPresent, got %v", err)
	}
}

func TestVerifyNODATAOptOut(t *testing.T) {
	recs := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"host.example.", []uint16{TypeA}, false},
		{"ns1.example.", []uint16{TypeA}, false},
	})
	// Flag exactly the record that brackets the unsigned delegation name.
	cover := markCoveringOptOut(t, recs, "opt.example.")
	v := newVerifier(t, recs)
	proof, err := v.VerifyNODATA("opt.example.", TypeDS)
	if err != nil {
		t.Fatalf("Opt-Out NODATA: %v", err)
	}
	if proof.Kind != ProofNODATAOptOut || proof.NextCloserRecord != cover ||
		!proof.NextCloserRecord.OptOut() {
		t.Fatalf("unexpected proof: %+v", proof)
	}
	// Opt-Out proves nothing for a non-DS query.
	_, err = v.VerifyNODATA("opt.example.", TypeA)
	if !errors.Is(err, ErrOptOutNotApplicable) {
		t.Fatalf("want ErrOptOutNotApplicable, got %v", err)
	}
}

func TestVerifyNODATADSRejectsCoverWithoutOptOut(t *testing.T) {
	recs := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"host.example.", []uint16{TypeA}, false},
		{"ns1.example.", []uint16{TypeA}, false},
	})
	v := newVerifier(t, recs)
	_, err := v.VerifyNODATA("unsigned.example.", TypeDS)
	if !errors.Is(err, ErrOptOutRequired) {
		t.Fatalf("want ErrOptOutRequired, got %v", err)
	}
}

func TestVerifyUnsignedDelegation(t *testing.T) {
	recs := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"host.example.", []uint16{TypeA}, false},
		{"ns1.example.", []uint16{TypeA}, false},
	})
	cover := markCoveringOptOut(t, recs, "del.example.")
	v := newVerifier(t, recs)
	proof, err := v.VerifyUnsignedDelegation("del.example.")
	if err != nil {
		t.Fatalf("VerifyUnsignedDelegation: %v", err)
	}
	if proof.Kind != ProofUnsignedDelegation || proof.NextCloserRecord != cover {
		t.Fatalf("unexpected proof: %+v", proof)
	}
	if proof.ClosestEncloser != "example." || !proof.NextCloserRecord.OptOut() {
		t.Fatal("proof did not use an Opt-Out covering record under the apex")
	}

	// Forgery: strip the flag from the very same covering record and
	// confirm a signed child cannot be downgraded to insecure.
	clearCoveringOptOut(t, recs, "del.example.")
	v2 := newVerifier(t, recs)
	if _, err := v2.VerifyUnsignedDelegation("del.example."); !errors.Is(err, ErrOptOutRequired) {
		t.Fatalf("forgery: want ErrOptOutRequired, got %v", err)
	}
}

func TestVerifyUnsignedDelegationRejectsDelegationEncloser(t *testing.T) {
	// sub.example is itself a delegation point (NS bit); a proof anchored
	// there must be rejected even when an Opt-Out record covers the NC.
	recs := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"sub.example.", []uint16{TypeNS}, false},
		{"host.sub.example.", []uint16{TypeA}, false},
	})
	markCoveringOptOut(t, recs, "x.sub.example.")
	v := newVerifier(t, recs)
	_, err := v.VerifyUnsignedDelegation("x.sub.example.")
	if !errors.Is(err, ErrDelegationPoint) {
		t.Fatalf("want ErrDelegationPoint, got %v", err)
	}
}

func TestVerifyWildcardAnswer(t *testing.T) {
	recs := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"host.example.", []uint16{TypeA}, false},
		{"*.example.", []uint16{TypeA}, false},
	})
	v := newVerifier(t, recs)
	proof, err := v.VerifyWildcardAnswer("anything.example.", "*.example.")
	if err != nil {
		t.Fatalf("VerifyWildcardAnswer: %v", err)
	}
	if proof.Kind != ProofWildcard || proof.NextCloserRecord == nil {
		t.Fatalf("unexpected proof: %+v", proof)
	}
	if proof.ClosestEncloser != "example." || proof.NextCloser != "anything.example." {
		t.Fatalf("triple = %q %q", proof.ClosestEncloser, proof.NextCloser)
	}

	// Build a closest encloser as an exact owner and a separate record
	// whose interval provably cannot contain the next closer hash. The
	// second record uses a wrapping (owner > next) sliver above the NC
	// hash, so the NC sits in the ring gap.
	ncHash := mustHash(t, "anything.example.")
	apexHash := mustHash(t, "example.")
	above := incrementBytes(ncHash, 0x40)
	below := incrementBytes(ncHash, -1)
	sparse := []*Record{
		{
			Algorithm: testAlg, Iterations: testIter, Salt: testSalt,
			OwnerHash: apexHash, NextHash: below,
			Types: typeSet([]uint16{TypeSOA}),
		},
		{
			Algorithm: testAlg, Iterations: testIter, Salt: testSalt,
			OwnerHash: above, NextHash: below,
			Types: typeSet([]uint16{TypeA}),
		},
	}
	for i, r := range sparse {
		if RecordCoversHash(r, ncHash) {
			t.Fatalf("test setup: record %d covers the next closer", i)
		}
	}
	if _, err := newVerifier(t, sparse).VerifyWildcardAnswer("anything.example.", ""); err == nil {
		t.Fatal("wildcard accepted without a next-closer covering record")
	} else if !errors.Is(err, ErrNextCloserNotCovered) {
		t.Fatalf("want ErrNextCloserNotCovered, got %v", err)
	}
}

func TestCrossZoneAndConstructionRejections(t *testing.T) {
	base := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA, TypeNS}, false},
		{"a.example.", []uint16{TypeA}, false},
	})
	if _, err := NewVerifier(nil); !errors.Is(err, ErrNoRecords) {
		t.Fatalf("empty: %v", err)
	}
	// Different salt => records from another zone/chain.
	foreign := &Record{
		Algorithm:  testAlg,
		Iterations: testIter,
		Salt:       []byte{0x01, 0x02},
		OwnerHash:  mustHash(t, "b.example."),
		NextHash:   base[0].OwnerHash,
		Types:      typeSet([]uint16{TypeA}),
	}
	if _, err := NewVerifier(append(base, foreign)); !errors.Is(err, ErrParamMismatch) {
		t.Fatalf("cross-zone: want ErrParamMismatch, got %v", err)
	}
	// Duplicate owner hash.
	dup := &Record{
		Algorithm: testAlg, Iterations: testIter, Salt: testSalt,
		OwnerHash: base[0].OwnerHash, NextHash: base[0].NextHash,
		Types: typeSet(nil),
	}
	if _, err := NewVerifier(append(base, dup)); !errors.Is(err, ErrDuplicateOwner) {
		t.Fatalf("duplicate: want ErrDuplicateOwner, got %v", err)
	}
	// Unknown algorithm at construction time.
	bad := &Record{Algorithm: 9, Iterations: 0, NextHash: hb(1), OwnerHash: hb(2),
		Types: map[uint16]bool{}}
	if _, err := NewVerifier([]*Record{bad}); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("unknown alg: %v", err)
	}
}
