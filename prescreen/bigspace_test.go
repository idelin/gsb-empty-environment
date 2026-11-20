package prescreen

import (
	"crypto/ed25519"
	"testing"
)

// fullSpaceBlocks is the number of distinct 48-bit block numbers: 2^48.
const fullSpaceBlocks uint64 = uint64(1) << Width

func singleHop(t *testing.T, k testKeys, tenant string, m AffineMap) (Policy, ObjectRef, ObjectRef, []string) {
	t.Helper()
	v1 := ObjectRef{Tenant: tenant, Object: "logical", Version: 1}
	v2 := ObjectRef{Tenant: tenant, Object: "physical", Version: 3}
	edge := ViewEdge{From: v1, To: v2, ParentVersion: v1.Version, Map: m}
	signed, err := SignView("project", edge, k.root)
	if err != nil {
		t.Fatal(err)
	}
	pol := policyWith([]ed25519.PublicKey{k.rootPub}, signed)
	return pol, v1, v2, []string{"project"}
}

func Test48BitFullSpaceNoEnumeration(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v2, chain := singleHop(t, k, "big", IdentityMap())

	// Request and grant each span all 2^48 blocks. Enumerating them is
	// impossible; admission only performs O(48^3) rank work.
	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)
	caps := []Capability{grantCap(t, k, v2, []Affine{FullSpace()})}

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("admission failed: %v", err)
	}
	ot := ticket.Objects[0]
	if ot.DistinctBlocks != fullSpaceBlocks {
		t.Fatalf("distinct = %d, want 2^48 = %d", ot.DistinctBlocks, fullSpaceBlocks)
	}
	if len(ot.PhysicalSets[0].Dir) != Width {
		t.Fatal("full-space physical set must have 48 independent directions")
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func Test48BitOverlappingGrantsAndRevocation(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v2, chain := singleHop(t, k, "big2", IdentityMap())

	// Split 48-bit space into two 47-dim affine halves by coordinate 5.
	halfA := SolveAffine([]Eq{MakeEq(Vec(1)<<5, 0)})
	halfB := SolveAffine([]Eq{MakeEq(Vec(1)<<5, 1)})
	if halfA.Size() != fullSpaceBlocks/2 || halfB.Size() != fullSpaceBlocks/2 {
		t.Fatal("each half must hold 2^47 blocks")
	}

	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)
	caps := []Capability{grantCap(t, k, v2, []Affine{halfA, halfB})}

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("union of halves must cover full space: %v", err)
	}
	if ticket.Objects[0].DistinctBlocks != fullSpaceBlocks {
		t.Fatalf("union count = %d, want 2^48", ticket.Objects[0].DistinctBlocks)
	}
	// Certificate exposes the disjoint intersection algebra (no overlap term).
	cert := ticket.Objects[0].Certificates[0]
	if cert.UnionSize != fullSpaceBlocks || cert.CoveredSize != fullSpaceBlocks {
		t.Fatal("coverage certificate must state 2^48 without enumerating")
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}

	// A single-point revocation on a 48-dim request is detected algebraically.
	revoked := Point(Vec(0x123456789abc) & Vec(fullMask))
	revCaps := append([]Capability(nil), caps...)
	revCaps = append(revCaps, revokeCap(t, k, v2, []Affine{revoked}))
	if _, err := Evaluate(pol, req, revCaps); err != ErrRevoked {
		t.Fatalf("single-point revocation must be caught, got %v", err)
	}
}

func Test48BitRankDropAliasCount(t *testing.T) {
	k := newTestKeys(t)
	// Project away three output coordinates: rank 45, every physical block has
	// 2^3 logical aliases.
	m := IdentityMap()
	m.Rows[10] = 0
	m.Rows[20] = 0
	m.Rows[40] = 0
	if m.Rank() != Width-3 {
		t.Fatalf("rank = %d, want 45", m.Rank())
	}
	pol, v1, v2, chain := singleHop(t, k, "big3", m)

	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)
	caps := []Capability{grantCap(t, k, v2, []Affine{FullSpace()})}

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(1) << 45
	if ticket.Objects[0].DistinctBlocks != want {
		t.Fatalf("rank-drop count = %d, want 2^45 = %d",
			ticket.Objects[0].DistinctBlocks, want)
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func Test48BitEqualSizeDifferentSetSemantics(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v2, chain := singleHop(t, k, "big4", IdentityMap())

	// Two distinct 47-dim hyperplanes: identical 2^47 cardinality, different
	// sets. A count-only check cannot tell them apart; the verifier must.
	planeEq0 := SolveAffine([]Eq{MakeEq(Vec(1)<<0, 0)})
	planeEq1 := SolveAffine([]Eq{MakeEq(Vec(1)<<1, 0)})
	if planeEq0.Size() != planeEq1.Size() {
		t.Fatal("setup: cardinalities must be equal")
	}
	if planeEq0.Equal(planeEq1) {
		t.Fatal("setup: sets must differ")
	}

	req := signedRequest(t, k, v1, []Affine{planeEq0}, chain)
	// Grant the other plane: same size, wrong set.
	wrong := []Capability{grantCap(t, k, v2, []Affine{planeEq1})}
	if _, err := Evaluate(pol, req, wrong); err != ErrNotCovered {
		t.Fatalf("equal-size wrong-set must be denied, got %v", err)
	}

	// Correct plane admits and verifies.
	good := []Capability{grantCap(t, k, v2, []Affine{planeEq0})}
	ticket, err := Evaluate(pol, req, good)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTicket(pol, req, good, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func Test48BitTwelveSlicesCompletesFast(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v2, chain := singleHop(t, k, "big5", IdentityMap())

	request := SolveAffine([]Eq{MakeEq(Vec(1)<<0, 0)})
	// Twelve overlapping grant subspaces exercise the maximal 2^12-1 term
	// inclusion-exclusion fan-out while each set spans 2^47 blocks. The final
	// slice is the request plane itself so coverage holds algebraically.
	grants := make([]Affine, 0, MaxSlices)
	for i := 1; i < MaxSlices; i++ {
		grants = append(grants, SolveAffine([]Eq{MakeEq(Vec(1)<<(i+1), 0)}))
	}
	grants = append(grants, request)
	req := signedRequest(t, k, v1, []Affine{request}, chain)

	// Split across two capabilities; the flattened total stays at MaxSlices.
	caps := []Capability{
		grantCap(t, k, v2, grants[:6]),
		grantCap(t, k, v2, grants[6:]),
	}
	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("twelve-slice admission failed: %v", err)
	}
	if ticket.Objects[0].DistinctBlocks != request.Size() {
		t.Fatalf("distinct = %d, want %d", ticket.Objects[0].DistinctBlocks, request.Size())
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func BenchmarkInclusionExclusion12Slices48Bit(b *testing.B) {
	sets := make([]Affine, MaxSlices)
	for i := range sets {
		sets[i] = SolveAffine([]Eq{MakeEq(Vec(1)<<(i+1), 0)})
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		proof := UnionInclusionExclusion(sets)
		if proof.UnionSize == 0 {
			b.Fatal("unexpected empty union")
		}
	}
}
