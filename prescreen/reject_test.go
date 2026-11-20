package prescreen

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestRejectionPaths(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-g")
	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)
	caps := []Capability{grantCap(t, k, v3, []Affine{FullSpace()})}

	t.Run("missing trusted root", func(t *testing.T) {
		bad := pol
		bad.Roots = []ed25519.PublicKey{k.otherPub}
		if _, err := Evaluate(bad, req, caps); !errors.Is(err, ErrUntrustedRoot) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("request signature tamper", func(t *testing.T) {
		tampered := req
		tampered.Signature = append([]byte(nil), req.Signature...)
		tampered.Signature[0] ^= 1
		if _, err := Evaluate(pol, tampered, caps); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("forged grant signature", func(t *testing.T) {
		forged, err := SignCapability(Capability{
			Kind: CapGrant, Subject: k.subjPub, Tenant: v3.Tenant,
			Object: v3, Action: ActionRead, Slices: []Affine{FullSpace()},
		}, k.other)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Evaluate(pol, req, []Capability{forged}); !errors.Is(err, ErrUntrustedRoot) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("view cycle", func(t *testing.T) {
		cyclic := pol
		manifests := append([]ViewManifest(nil), pol.Manifests...)
		edge := manifests[1].Edge
		edge.To = v1
		signed, err := SignView(manifests[1].Name, edge, k.root)
		if err != nil {
			t.Fatal(err)
		}
		manifests[1] = signed
		cyclic.Manifests = manifests
		if _, err := Evaluate(cyclic, req, caps); !errors.Is(err, ErrViewCycle) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		// Keep the request on the real v1 object so the edge is found, then
		// re-sign the edge with a stale ParentVersion to force the version
		// binding check rather than an object-resolution failure.
		stalePol := pol
		manifests := append([]ViewManifest(nil), pol.Manifests...)
		edge := manifests[0].Edge
		edge.ParentVersion = v1.Version + 100
		signed, err := SignView(manifests[0].Name, edge, k.root)
		if err != nil {
			t.Fatal(err)
		}
		manifests[0] = signed
		stalePol.Manifests = manifests
		if _, err := Evaluate(stalePol, req, caps); !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("foreign subject grants ignored", func(t *testing.T) {
		foreign, err := SignCapability(Capability{
			Kind: CapGrant, Subject: k.otherPub, Tenant: v3.Tenant,
			Object: v3, Action: ActionRead, Slices: []Affine{FullSpace()},
		}, k.root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Evaluate(pol, req, []Capability{foreign}); !errors.Is(err, ErrNotCovered) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("action mismatch", func(t *testing.T) {
		bad := req
		bad.Action = Action("write")
		if _, err := Evaluate(pol, bad, caps); !errors.Is(err, ErrActionMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("unknown view edge", func(t *testing.T) {
		bad := signedRequest(t, k, v1, []Affine{FullSpace()}, []string{"nope"})
		if _, err := Evaluate(pol, bad, caps); !errors.Is(err, ErrViewNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestSliceLimitsAndEmptySlices(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-h")

	many := make([]Affine, MaxSlices+1)
	for i := range many {
		many[i] = Point(Vec(i))
	}
	badReq := signedRequest(t, k, v1, many, chain)
	if _, err := Evaluate(pol, badReq, nil); !errors.Is(err, ErrMalformedRequest) {
		t.Fatalf("got %v", err)
	}

	overCap := grantCap(t, k, v3, many)
	if err := verifyCapability(pol, overCap); !errors.Is(err, ErrTooManySlices) {
		t.Fatalf("got %v", err)
	}

	// A request whose image is entirely collapsed to the empty set vacuously
	// admits with zero distinct blocks.
	emptyReq := signedRequest(t, k, v1, []Affine{EmptyAffine()}, chain)
	ticket, err := Evaluate(pol, emptyReq, nil)
	if err != nil {
		t.Fatalf("empty-only request should admit: %v", err)
	}
	if ticket.Objects[0].DistinctBlocks != 0 || len(ticket.Objects[0].Certificates) != 0 {
		t.Fatalf("empty request should yield zero blocks and no certs")
	}
	if err := VerifyTicket(pol, emptyReq, nil, ticket); err != nil {
		t.Fatalf("empty ticket should verify: %v", err)
	}
}

func TestCertificateTamperRejected(t *testing.T) {
	k := newTestKeys(t)
	pol, v1, v3, chain := buildTwoHopChain(t, k, "tenant-i")

	halfA := SolveAffine([]Eq{MakeEq(Vec(1)<<3, 0)})
	halfB := SolveAffine([]Eq{MakeEq(Vec(1)<<3, 1)})
	caps := []Capability{grantCap(t, k, v3, []Affine{halfA, halfB})}
	req := signedRequest(t, k, v1, []Affine{FullSpace()}, chain)

	ticket, err := Evaluate(pol, req, caps)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("flipped cardinality", func(t *testing.T) {
		bad := *ticket
		bad.Objects = append([]ObjectTicket(nil), ticket.Objects...)
		bad.Objects[0].DistinctBlocks++
		if err := VerifyTicket(pol, req, caps, &bad); !errors.Is(err, ErrTicketMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("forged certificate term", func(t *testing.T) {
		bad := *ticket
		bad.Objects = append([]ObjectTicket(nil), ticket.Objects...)
		terms := append([]IntersectionCertificate(nil), ticket.Objects[0].UnionTerms...)
		terms[0].Cardinality++
		bad.Objects[0].UnionTerms = terms
		// Semantic comparison against the recomputed ticket fails closed first.
		if err := VerifyTicket(pol, req, caps, &bad); !errors.Is(err, ErrTicketMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("request hash binding", func(t *testing.T) {
		bad := *ticket
		bad.RequestHash = append([]byte(nil), ticket.RequestHash...)
		bad.RequestHash[0] ^= 1
		if err := VerifyTicket(pol, req, caps, &bad); !errors.Is(err, ErrTicketMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("independent algebra rejects forged intersection", func(t *testing.T) {
		// independentlyCheckObject recomputes the algebra from raw slices, so a
		// same-size set swap that survives a naive count check must fail here.
		bad := *ticket
		bad.Objects = append([]ObjectTicket(nil), ticket.Objects...)
		certs := append([]SliceCertificate(nil), ticket.Objects[0].Certificates...)
		terms := append([]IntersectionCertificate(nil), certs[0].GrantTerms...)
		for i := range terms {
			terms[i].Intersect = FullSpace()
			terms[i].Cardinality = terms[i].Intersect.Size()
		}
		certs[0].GrantTerms = terms
		bad.Objects[0].Certificates = certs
		if err := independentlyCheckObject(bad.Objects[0]); !errors.Is(err, ErrCertificate) {
			t.Fatalf("independent algebra must reject forged set, got %v", err)
		}

		// The genuine object ticket passes the same independent check.
		if err := independentlyCheckObject(ticket.Objects[0]); err != nil {
			t.Fatalf("genuine ticket must pass independent check: %v", err)
		}
	})
}
