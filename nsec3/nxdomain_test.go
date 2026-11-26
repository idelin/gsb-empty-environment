package nsec3

import (
	"errors"
	"testing"
)

// rawRec builds an NSEC3 record from a named owner (hashed) with an
// explicit raw NextHash, giving deterministic ring geometry.
func rawRec(t *testing.T, ownerName string, nextHash []byte, types []uint16, optOut bool) *Record {
	t.Helper()
	flags := uint8(0)
	if optOut {
		flags = optOutFlag
	}
	return &Record{
		Algorithm:  testAlg,
		Flags:      flags,
		Iterations: testIter,
		Salt:       append([]byte(nil), testSalt...),
		OwnerHash:  mustHash(t, ownerName),
		NextHash:   nextHash,
		Types:      typeSet(types),
	}
}

// coveringRecordFor returns a record whose interval deterministically
// covers targetHash, using anchorName's hash as the interval owner.
// The caller chooses anchorName so its hash is strictly below targetHash
// (checked here); NextHash is set exactly to targetHash, which is an
// endpoint and therefore covered-or-not is decided by the strict rule.
func coveringRecordFor(t *testing.T, anchorName string, targetHash []byte, types []uint16, optOut bool) *Record {
	t.Helper()
	owner := mustHash(t, anchorName)
	if compareBytes(owner, targetHash) >= 0 {
		t.Fatalf("anchor %s hash must be strictly below target", anchorName)
	}
	// Coverage is the open interval (owner, next); set next to target+1
	// so the target is the greatest covered value (160-bit wrap handled).
	next := append([]byte(nil), targetHash...)
	carry := 1
	for i := len(next) - 1; i >= 0 && carry > 0; i-- {
		sum := int(next[i]) + carry
		next[i] = byte(sum)
		carry = sum >> 8
	}
	return rawRec(t, anchorName, next, types, optOut)
}

// buildNXDOMAINProof constructs a valid NXDOMAIN triple for
// "x.y.example." by anchoring two covering intervals below the next
// closer and wildcard hashes respectively.
func buildNXDOMAINProof(t *testing.T) []*Record {
	t.Helper()
	apexHash := mustHash(t, "example.")
	ncHash := mustHash(t, "y.example.")
	wildHash := mustHash(t, "*.example.")

	// Anchors are distinct, previously-unused names whose hashes we rely
	// on only to be owners; the NextHash pins the covered endpoint.
	ncCover := coveringRecordFor(t, "anchor-nc.example.", ncHash, []uint16{TypeA}, false)
	wildCover := coveringRecordFor(t, "anchor-wild.example.", wildHash, []uint16{TypeA}, false)

	// Closest encloser: exact owner for the apex.
	ceRec := rawRec(t, "example.", ncCover.OwnerHash, []uint16{TypeSOA, TypeNS}, false)

	// Sanity-check the geometry the verifier depends on.
	if !RecordCoversHash(ncCover, ncHash) {
		t.Fatal("setup: NC record does not cover next closer")
	}
	if !RecordCoversHash(wildCover, wildHash) {
		t.Fatal("setup: wildcard record does not cover wildcard")
	}
	if string(ceRec.OwnerHash) != string(apexHash) {
		t.Fatal("setup: closest encloser owner mismatch")
	}
	return []*Record{ceRec, ncCover, wildCover}
}

func TestVerifyNXDOMAIN(t *testing.T) {
	recs := buildNXDOMAINProof(t)
	v := newVerifier(t, recs)
	proof, err := v.VerifyNXDOMAIN("x.y.example.")
	if err != nil {
		t.Fatalf("VerifyNXDOMAIN: %v", err)
	}
	if proof.Kind != ProofNXDOMAIN {
		t.Fatalf("kind = %d", proof.Kind)
	}
	if proof.ClosestEncloser != "example." || proof.NextCloser != "y.example." ||
		proof.Wildcard != "*.example." {
		t.Fatalf("triple = %q %q %q", proof.ClosestEncloser, proof.NextCloser, proof.Wildcard)
	}
	if proof.Encloser == nil || proof.NextCloserRecord == nil || proof.WildcardRecord == nil {
		t.Fatal("missing record in proof triple")
	}
	if string(proof.NextCloserRecord.OwnerHash) == string(mustHash(t, "y.example.")) {
		t.Fatal("next closer was exact-matched instead of covered")
	}
	if string(proof.WildcardRecord.OwnerHash) == string(mustHash(t, "*.example.")) {
		t.Fatal("wildcard was exact-matched instead of covered")
	}
}

func TestVerifyNXDOMAINRejectsMissingWildcard(t *testing.T) {
	recs := buildNXDOMAINProof(t)
	wildHash := mustHash(t, "*.example.")
	// Insert a record whose owner is exactly the wildcard hash. An exact
	// owner cannot simultaneously be a covered name, so the NXDOMAIN
	// wildcard leg must fail.
	wildRec := &Record{
		Algorithm: testAlg, Iterations: testIter, Salt: testSalt,
		OwnerHash: wildHash,
		NextHash:  recs[0].OwnerHash,
		Types:     typeSet([]uint16{TypeA}),
	}
	recs = append(recs, wildRec)
	v, err := NewVerifier(recs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyNXDOMAIN("x.y.example."); !errors.Is(err, ErrWildcardNotCovered) {
		t.Fatalf("want ErrWildcardNotCovered, got %v", err)
	}
}

func TestVerifyNXDOMAINRejectsMissingClosestEncloser(t *testing.T) {
	recs := buildNXDOMAINProof(t)
	apexHash := mustHash(t, "example.")
	kept := make([]*Record, 0, len(recs))
	for _, r := range recs {
		if string(r.OwnerHash) == string(apexHash) {
			continue
		}
		kept = append(kept, r)
	}
	v, err := NewVerifier(kept)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyNXDOMAIN("x.y.example."); !errors.Is(err, ErrNoClosestEncloser) {
		t.Fatalf("want ErrNoClosestEncloser, got %v", err)
	}
}

func TestVerifyNXDOMAINRejectsExistingWildcard(t *testing.T) {
	recs := buildNXDOMAINProof(t)
	wildHash := mustHash(t, "*.example.")
	wildRec := &Record{
		Algorithm: testAlg, Iterations: testIter, Salt: testSalt,
		OwnerHash: wildHash,
		NextHash:  recs[0].OwnerHash,
		Types:     typeSet([]uint16{TypeA}),
	}
	v, err := NewVerifier(append(recs, wildRec))
	if err != nil {
		t.Fatal(err)
	}
	// The wildcard is now an exact owner, so an NXDOMAIN claim fails.
	if _, err := v.VerifyNXDOMAIN("y.example."); !errors.Is(err, ErrWildcardNotCovered) {
		t.Fatalf("want ErrWildcardNotCovered, got %v", err)
	}
}
