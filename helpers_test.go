package netsec

import (
	"math/big"
	"testing"
	"time"
)

var (
	now      = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	hashBits = new(big.Int).Lsh(big.NewInt(1), 160)
	maxHash  = new(big.Int).Sub(new(big.Int).Set(hashBits), big.NewInt(1))
)

func testApex(t *testing.T) Name {
	t.Helper()
	return MustParseName("example.")
}

func testPolicy(view TenantView) Policy {
	return Policy{
		View:               view,
		Generation:         7,
		KeyGeneration:      3,
		MaxNSEC3Iterations: DefaultNSEC3MaxIterations,
		AllowNSEC3OptOut:   true,
	}
}

func testAttestation(view TenantView, apex Name, params Parameters, keyGen, policyGen uint64) Attestation {
	return Attestation{
		View:               view,
		Apex:               apex,
		Class:              ClassIN,
		KeyGeneration:      keyGen,
		PolicyGeneration:   policyGen,
		Parameters:         params,
		ChainVerified:      true,
		SignaturesVerified: true,
		ValidFrom:          now.Add(-time.Hour),
		ValidUntil:         now.Add(2 * time.Hour),
	}
}

func testRRSIG(covered uint16, apex Name, keyGen uint64) RRSIG {
	return RRSIG{
		TypeCovered:   covered,
		Algorithm:     8,
		KeyTag:        0x1234,
		Signer:        apex,
		KeyGeneration: keyGen,
		ValidFrom:     now.Add(-time.Hour),
		ValidUntil:    now.Add(2 * time.Hour),
		Verified:      true,
	}
}

func testResponse(view TenantView, apex Name, qname Name, qtype uint16, params Parameters, keyGen, policyGen uint64) Response {
	return Response{
		Attestation: testAttestation(view, apex, params, keyGen, policyGen),
		Question:    Question{Name: qname, Type: qtype, Class: ClassIN},
	}
}

func testNSEC(t *testing.T, apex Name, owner, next string, types TypeBitmap, keyGen uint64) NSECRecord {
	t.Helper()
	return NSECRecord{
		Owner: MustParseName(owner),
		Next:  MustParseName(next),
		Types: types,
		Class: ClassIN,
		TTL:   300,
		RRSIG: testRRSIG(TypeNSEC, apex, keyGen),
	}
}

func mustHash(t *testing.T, p Parameters, name Name) []byte {
	t.Helper()
	h, err := p.HashName(name)
	if err != nil {
		t.Fatalf("HashName(%s): %v", name, err)
	}
	return h
}

func testNSEC3Hash(t *testing.T, apex Name, p Parameters, ownerHash, nextHash []byte, types TypeBitmap, optOut bool, keyGen uint64) NSEC3Record {
	t.Helper()
	owner, err := NSEC3OwnerName(apex, ownerHash)
	if err != nil {
		t.Fatalf("NSEC3OwnerName: %v", err)
	}
	return NSEC3Record{
		Owner:           owner,
		NextHashedOwner: append([]byte(nil), nextHash...),
		HashAlgorithm:   p.HashAlgorithm,
		Iterations:      p.Iterations,
		Salt:            append([]byte(nil), p.Salt...),
		OptOut:          optOut,
		Types:           types,
		Class:           ClassIN,
		TTL:             300,
		RRSIG:           testRRSIG(TypeNSEC3, apex, keyGen),
	}
}

func testNSEC3Name(t *testing.T, apex Name, p Parameters, name Name, nextHash []byte, types TypeBitmap, optOut bool, keyGen uint64) NSEC3Record {
	t.Helper()
	return testNSEC3Hash(t, apex, p, mustHash(t, p, name), nextHash, types, optOut, keyGen)
}

func nsecNXResponse(t *testing.T, view TenantView, keyGen, policyGen uint64) Response {
	t.Helper()
	apex := testApex(t)
	qname := MustParseName("ghost.example.")
	resp := testResponse(view, apex, qname, TypeA, ParamsNSEC(), keyGen, policyGen)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, "g.example.", "h.example.", MustTypeBitmap(TypeA, TypeRRSIG, TypeNSEC), keyGen),
		testNSEC(t, apex, "example.", "a.example.", MustTypeBitmap(TypeSOA, TypeNS, TypeRRSIG, TypeNSEC), keyGen),
	}
	return resp
}

func nsecNODATAExactResponse(t *testing.T, view TenantView, name Name, keyGen, policyGen uint64) Response {
	t.Helper()
	apex := testApex(t)
	resp := testResponse(view, apex, name, TypeA, ParamsNSEC(), keyGen, policyGen)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, name.String(), "h.example.", MustTypeBitmap(TypeAAAA, TypeRRSIG, TypeNSEC), keyGen),
	}
	return resp
}

func nsec3NXResponse(t *testing.T, view TenantView, qname Name, params Parameters, keyGen, policyGen uint64) Response {
	t.Helper()
	apex := testApex(t)
	ceHash := mustHash(t, params, apex)
	nextHash := mustHash(t, params, qname)
	nextOwner, nextNext := coverHashes(t, nextHash)
	wildcard := MustParseName("*.example.")
	wildHash := mustHash(t, params, wildcard)
	wildOwner, wildNext := coverHashes(t, wildHash)
	resp := testResponse(view, apex, qname, TypeA, params, keyGen, policyGen)
	resp.NSEC3 = []NSEC3Record{
		testNSEC3Hash(t, apex, params, ceHash, ceHash, MustTypeBitmap(TypeSOA, TypeNS), false, keyGen),
		testNSEC3Hash(t, apex, params, nextOwner, nextNext, MustTypeBitmap(), false, keyGen),
		testNSEC3Hash(t, apex, params, wildOwner, wildNext, MustTypeBitmap(), false, keyGen),
	}
	return resp
}

// coverHashes returns a short circular NSEC3 interval that strictly covers
// target. It is used to model authenticated interval RRsets in tests.
func coverHashes(t *testing.T, target []byte) ([]byte, []byte) {
	t.Helper()
	if len(target) != 20 {
		t.Fatalf("target hash length %d", len(target))
	}
	v := new(big.Int).SetBytes(target)
	owner := new(big.Int)
	next := new(big.Int)
	switch {
	case v.Sign() == 0:
		owner.Set(maxHash)
		next.SetInt64(1)
	case v.Cmp(maxHash) == 0:
		owner.Sub(v, big.NewInt(1))
		next.SetInt64(0)
	default:
		owner.Sub(v, big.NewInt(1))
		next.Add(v, big.NewInt(1))
	}
	return pad20(owner), pad20(next)
}

func pad20(v *big.Int) []byte {
	out := make([]byte, 20)
	raw := v.Bytes()
	copy(out[len(out)-len(raw):], raw)
	return out
}

type fakeClock struct {
	when time.Time
}

func (f *fakeClock) Now() time.Time { return f.when }
