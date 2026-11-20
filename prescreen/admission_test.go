package prescreen

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func dropBitMap(bit int) AffineMap {
	m := IdentityMap()
	m.Rows[bit] = 0 // output bit is always zero -> rank drops by one
	return m
}

func grantCap(t *testing.T, k testKeys, obj ObjectRef, slices []Affine) Capability {
	t.Helper()
	c, err := SignCapability(Capability{
		Kind:    CapGrant,
		Subject: k.subjPub,
		Tenant:  obj.Tenant,
		Object:  obj,
		Action:  ActionRead,
		Slices:  slices,
	}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func revokeCap(t *testing.T, k testKeys, obj ObjectRef, slices []Affine) Capability {
	t.Helper()
	c, err := SignCapability(Capability{
		Kind:    CapRevoke,
		Subject: k.subjPub,
		Tenant:  obj.Tenant,
		Object:  obj,
		Action:  ActionRead,
		Slices:  slices,
	}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// buildTwoHopChain creates v1 --e1--> v2 --e2--> v3 with rank-deficient maps.
func buildTwoHopChain(t *testing.T, k testKeys, tenant string) (Policy, ObjectRef, ObjectRef, []string) {
	t.Helper()
	v1 := ObjectRef{Tenant: tenant, Object: "logical", Version: 1}
	v2 := ObjectRef{Tenant: tenant, Object: "mid", Version: 7}
	v3 := ObjectRef{Tenant: tenant, Object: "physical", Version: 9}

	e1 := ViewEdge{From: v1, To: v2, ParentVersion: v1.Version, Map: dropBitMap(0)}
	e2 := ViewEdge{From: v2, To: v3, ParentVersion: v2.Version, Map: dropBitMap(1)}
	m1, err := SignView("hop1", e1, k.root)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := SignView("hop2", e2, k.root)
	if err != nil {
		t.Fatal(err)
	}
	pol := policyWith([]ed25519.PublicKey{k.rootPub}, m1, m2)
	return pol, v1, v3, []string{"hop1", "hop2"}
}

func signedRequest(t *testing.T, k testKeys, obj ObjectRef, slices []Affine, chain []string) ReadRequest {
	t.Helper()
	req, err := SignRequest(ReadRequest{
		Tenant: obj.Tenant,
		Object: obj,
		Action: ActionRead,
		Slices: slices,
		Chain:  chain,
	}, k.subj)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestMultiLayerRankDropAndDistinctCount(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-a")

	// Full logical space through two rank-dropping edges lands in a 46-dim set.
	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)
	caps := []Capability{grantCap(t, k, v3, []Affine{FullSpace()})}

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("admission failed: %v", err)
	}
	objTicket := ticket.Objects[0]
	want := uint64(1) << 46
	if objTicket.DistinctBlocks != want {
		t.Fatalf("distinct blocks = %d, want %d", objTicket.DistinctBlocks, want)
	}
	if objTicket.PhysicalSets[0].Dim() != 46 {
		t.Fatalf("physical dim = %d, want 46", objTicket.PhysicalSets[0].Dim())
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}
}

func TestDisjointLogicalSlicesSharePhysicalAlias(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-b")

	// Bit 0 is erased by the first edge, so the kernel vector e0 maps to zero.
	kernelVec := Vec(1) << 0
	a := Point(0)
	b := Point(kernelVec) // disjoint singleton from a, same physical image
	if a.Equal(b) {
		t.Fatal("test setup: logical slices must be disjoint")
	}
	req := signedRequest(t, k, v1, []Affine{a, b}, chain)
	caps := []Capability{grantCap(t, k, v3, []Affine{FullSpace()})}

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("admission failed: %v", err)
	}
	ot := ticket.Objects[0]
	// Two disjoint logical slices collapse onto one physical block.
	if ot.DistinctBlocks != 1 {
		t.Fatalf("alias not deduplicated: distinct=%d", ot.DistinctBlocks)
	}
	if len(ot.PhysicalSets) != 1 || len(ot.Certificates) != 2 {
		t.Fatalf("one distinct physical set expected, two slice certs; got %d/%d",
			len(ot.PhysicalSets), len(ot.Certificates))
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func TestEqualCardinalityDifferentSetRejected(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-c")

	request := Point(Vec(1) << 2)
	req := signedRequest(t, k, v1, []Affine{request}, chain)

	// A different singleton with the same cardinality (1) but a different
	// physical block: count-only verification would wrongly accept.
	wrongGrant := Point(Vec(1) << 3)
	caps := []Capability{grantCap(t, k, v3, []Affine{wrongGrant})}

	if _, err := Evaluate(pol, req, caps); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("want ErrNotCovered, got %v", err)
	}

	// A forged ticket carrying the right count but the wrong set is rejected.
	goodCaps := []Capability{grantCap(t, k, v3, []Affine{FullSpace()})}
	ticket, err := Evaluate(pol, req, goodCaps)
	if err != nil {
		t.Fatal(err)
	}
	ticket.Objects[0].PhysicalSets[0] = wrongGrant
	if err := VerifyTicket(pol, req, goodCaps, ticket); !errors.Is(err, ErrTicketMismatch) {
		t.Fatalf("verifier must compare semantics, got %v", err)
	}
}

func TestSameSetDifferentEquationsAdmitted(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-d")

	request := NewAffine([]Vec{Vec(1) << 4}, 0)
	req := signedRequest(t, k, v1, []Affine{request}, chain)

	composed := Compose(dropBitMap(1), dropBitMap(0))
	trueImage := composed.Apply(request)

	// Re-derive the same 1-dimensional image through a hand-written redundant
	// equation system rather than the canonical direction basis. Every
	// coordinate except the surviving direction bit 4 is pinned to zero.
	eqs := make([]Eq, 0, Width-1)
	for bit := 0; bit < Width; bit++ {
		if bit == 4 {
			continue
		}
		eqs = append(eqs, MakeEq(Vec(1)<<bit, 0))
	}
	imageSet := SolveAffine(eqs)
	if !imageSet.Equal(trueImage) {
		t.Fatal("test setup: hand-built equations must equal the mapped image")
	}
	caps := []Capability{grantCap(t, k, v3, []Affine{imageSet})}

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("equivalent equation representation must cover: %v", err)
	}
	if ticket.Objects[0].DistinctBlocks != imageSet.Size() {
		t.Fatal("counts should match for equivalent sets")
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func TestSinglePointRevocation(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-e")

	request := Point(Vec(1) << 5)
	req := signedRequest(t, k, v1, []Affine{request}, chain)

	physical := Compose(dropBitMap(1), dropBitMap(0)).Apply(request)
	if physical.Dim() != 0 {
		t.Fatal("setup: request must map to a single physical point")
	}
	caps := []Capability{
		grantCap(t, k, v3, []Affine{FullSpace()}),
		revokeCap(t, k, v3, []Affine{physical}),
	}
	if _, err := Evaluate(pol, req, caps); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}

	// Revoking a different singleton leaves the request admissible.
	otherCaps := []Capability{
		grantCap(t, k, v3, []Affine{FullSpace()}),
		revokeCap(t, k, v3, []Affine{Point(Vec(1) << 2)}),
	}
	ticket, err := Evaluate(pol, req, otherCaps)
	if err != nil {
		t.Fatalf("unrelated revocation must not block: %v", err)
	}
	if err := VerifyTicket(pol, req, otherCaps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func TestOverlappingGrantUnionCovers(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-f")

	// Two complementary 47-dim affine halves whose union covers the space:
	// neither alone contains the request, their union does.
	halfA := SolveAffine([]Eq{MakeEq(Vec(1)<<2, 0)})
	halfB := SolveAffine([]Eq{MakeEq(Vec(1)<<2, 1)})
	if !halfA.Intersect(halfB).Empty {
		t.Fatal("setup: complementary affine sets must be disjoint")
	}
	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)

	if _, err := Evaluate(pol, req, []Capability{grantCap(t, k, v3, []Affine{halfA})}); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("single half must not cover full space, got %v", err)
	}
	caps := []Capability{grantCap(t, k, v3, []Affine{halfA, halfB})}
	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatalf("grant union should cover: %v", err)
	}
	want := uint64(1) << 46
	if ticket.Objects[0].DistinctBlocks != want {
		t.Fatalf("distinct = %d want %d", ticket.Objects[0].DistinctBlocks, want)
	}
	if err := VerifyTicket(pol, req, caps, ticket); err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}
