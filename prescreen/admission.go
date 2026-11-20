package prescreen

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
)

// Evaluate admits a signed batch read against the policy and capabilities.
// On success it returns a ReadTicket that contains only the physical affine
// sets actually needed, exact distinct block counts and algebraic coverage
// certificates; block numbers are never expanded.
func Evaluate(p Policy, req ReadRequest, caps []Capability) (*ReadTicket, error) {
	prepared, err := prepare(p, req, caps)
	if err != nil {
		return nil, err
	}
	return prepared.ticket, nil
}

type prepared struct {
	ticket *ReadTicket
}

func prepare(p Policy, req ReadRequest, caps []Capability) (*prepared, error) {
	if req.Action != ActionRead {
		return nil, ErrActionMismatch
	}
	if len(req.Slices) == 0 || len(req.Slices) > MaxSlices {
		return nil, ErrMalformedRequest
	}
	if len(req.Subject) != ed25519.PublicKeySize || len(req.Signature) != ed25519.SignatureSize {
		return nil, ErrBadSignature
	}
	if !ed25519.Verify(req.Subject, requestSigningBytes(req), req.Signature) {
		return nil, ErrBadSignature
	}
	if req.Tenant != req.Object.Tenant {
		return nil, ErrTenantMismatch
	}

	chain, err := resolveChain(p, req.Object, req.Chain)
	if err != nil {
		return nil, err
	}

	grants, err := flattenCaps(p, caps, CapGrant, req.Subject, req.Tenant, chain.terminal, req.Action)
	if err != nil {
		return nil, err
	}
	revocations, err := flattenCaps(p, caps, CapRevoke, req.Subject, req.Tenant, chain.terminal, req.Action)
	if err != nil {
		return nil, err
	}
	if len(grants) > MaxSlices || len(revocations) > MaxSlices {
		return nil, ErrTooManySlices
	}

	grantSets := make([]Affine, len(grants))
	for i, g := range grants {
		grantSets[i] = g.slice
	}
	revokeSets := make([]Affine, len(revocations))
	for i, r := range revocations {
		revokeSets[i] = r.slice
	}

	certs := make([]SliceCertificate, 0, len(req.Slices))
	seen := map[string]int{}
	distinct := make([]Affine, 0, len(req.Slices))

	for _, logical := range req.Slices {
		physical := chain.composed.Apply(logical)
		if physical.Empty {
			continue
		}

		cert, err := buildSliceCertificate(physical, grantSets, revokeSets)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)

		if _, ok := seen[affineKey(physical)]; !ok {
			seen[affineKey(physical)] = len(distinct)
			distinct = append(distinct, physical)
		}
	}

	unionProof := UnionInclusionExclusion(distinct)
	terms := make([]IntersectionCertificate, len(unionProof.Terms))
	for i, t := range unionProof.Terms {
		terms[i] = IntersectionCertificate{
			Sets:        t.Sets,
			Intersect:   t.Aff,
			Sign:        signFor(len(t.Sets)),
			Cardinality: t.Aff.Size(),
		}
	}

	ticket := &ReadTicket{
		Tenant:      req.Tenant,
		Object:      req.Object,
		Terminal:    chain.terminal,
		RequestHash: hashRequest(req),
		Objects: []ObjectTicket{{
			Object:         chain.terminal,
			PhysicalSets:   distinct,
			DistinctBlocks: unionProof.UnionSize,
			UnionTerms:     terms,
			Certificates:   certs,
		}},
	}
	return &prepared{ticket: ticket}, nil
}

func signFor(n int) int8 {
	if n&1 == 1 {
		return 1
	}
	return -1
}

func buildSliceCertificate(physical Affine, grants, revocations []Affine) (SliceCertificate, error) {
	var bad []Affine
	for _, rev := range revocations {
		if hit := physical.Intersect(rev); !hit.Empty {
			bad = append(bad, hit)
		}
	}
	if len(bad) != 0 {
		return SliceCertificate{}, ErrRevoked
	}

	coveredSize, unionSize, proof, ok := CoverUnion(physical, grants)
	if !ok {
		return SliceCertificate{}, ErrNotCovered
	}

	terms := make([]IntersectionCertificate, len(proof.Terms))
	for i, t := range proof.Terms {
		terms[i] = IntersectionCertificate{
			Sets:        t.Sets,
			Intersect:   t.Aff,
			Sign:        signFor(len(t.Sets)),
			Cardinality: t.Aff.Size(),
		}
	}

	return SliceCertificate{
		Physical:            physical,
		GrantSlices:         cloneAffines(grants),
		GrantTerms:          terms,
		CoveredSize:         coveredSize,
		UnionSize:           unionSize,
		RevocationSlices:    cloneAffines(revocations),
		RevokedIntersection: nil,
	}, nil
}

func cloneAffines(in []Affine) []Affine {
	if len(in) == 0 {
		return nil
	}
	out := make([]Affine, len(in))
	copy(out, in)
	return out
}

func hashRequest(req ReadRequest) []byte {
	h := sha256.Sum256(requestSigningBytes(req))
	return h[:]
}

func sameAffines(a, b []Affine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func sameTerms(a, b []IntersectionCertificate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameInts(a[i].Sets, b[i].Sets) ||
			!a[i].Intersect.Equal(b[i].Intersect) ||
			a[i].Sign != b[i].Sign ||
			a[i].Cardinality != b[i].Cardinality {
			return false
		}
	}
	return true
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }
