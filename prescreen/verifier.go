package prescreen

// VerifyTicket is the independent consumer-side check. It never trusts the
// supplied ticket's counts or certificates: it re-resolves the signed view
// chain, re-verifies every Ed25519 signature, re-runs the rank and
// inclusion-exclusion algebra from the raw capabilities, and then requires
// full semantic equality of the physical sets and certificates. A ticket with
// the right block count but a different set is rejected.
func VerifyTicket(p Policy, req ReadRequest, caps []Capability, ticket *ReadTicket) error {
	if ticket == nil {
		return ErrCertificate
	}

	expected, err := prepare(p, req, caps)
	if err != nil {
		return err
	}
	want := expected.ticket

	if ticket.Tenant != want.Tenant || ticket.Object != want.Object ||
		ticket.Terminal != want.Terminal ||
		!bytesEqual(ticket.RequestHash, want.RequestHash) {
		return ErrTicketMismatch
	}
	if len(ticket.Objects) != len(want.Objects) {
		return ErrTicketMismatch
	}
	for i := range want.Objects {
		if err := compareObjectTicket(ticket.Objects[i], want.Objects[i]); err != nil {
			return err
		}
	}

	// Independently re-check every embedded certificate against the slices it
	// claims, rather than accepting the producer's arithmetic.
	for _, obj := range ticket.Objects {
		if err := independentlyCheckObject(obj); err != nil {
			return err
		}
	}
	return nil
}

func compareObjectTicket(got, want ObjectTicket) error {
	if got.Object != want.Object ||
		got.DistinctBlocks != want.DistinctBlocks ||
		!sameAffines(got.PhysicalSets, want.PhysicalSets) ||
		!sameTerms(got.UnionTerms, want.UnionTerms) {
		return ErrTicketMismatch
	}
	if len(got.Certificates) != len(want.Certificates) {
		return ErrTicketMismatch
	}
	for i := range want.Certificates {
		if err := compareSliceCertificate(got.Certificates[i], want.Certificates[i]); err != nil {
			return err
		}
	}
	return nil
}

func compareSliceCertificate(got, want SliceCertificate) error {
	if !got.Physical.Equal(want.Physical) ||
		got.CoveredSize != want.CoveredSize ||
		got.UnionSize != want.UnionSize ||
		!sameAffines(got.GrantSlices, want.GrantSlices) ||
		!sameAffines(got.RevocationSlices, want.RevocationSlices) ||
		!sameTerms(got.GrantTerms, want.GrantTerms) ||
		len(got.RevokedIntersection) != len(want.RevokedIntersection) {
		return ErrTicketMismatch
	}
	for i := range want.RevokedIntersection {
		if !got.RevokedIntersection[i].Equal(want.RevokedIntersection[i]) {
			return ErrTicketMismatch
		}
	}
	return nil
}

// independentlyCheckObject recomputes the algebra strictly from the affine
// slices carried by the certificate, proving the claims rather than comparing
// two producer outputs.
func independentlyCheckObject(obj ObjectTicket) error {
	// Recompute the distinct-block union from the raw physical sets.
	proof := UnionInclusionExclusion(obj.PhysicalSets)
	if proof.UnionSize != obj.DistinctBlocks || len(proof.Terms) != len(obj.UnionTerms) {
		return ErrCertificate
	}
	for i, t := range proof.Terms {
		claim := obj.UnionTerms[i]
		if !sameInts(t.Sets, claim.Sets) ||
			!t.Aff.Equal(claim.Intersect) ||
			t.Aff.Size() != claim.Cardinality ||
			claim.Sign != signFor(len(t.Sets)) {
			return ErrCertificate
		}
	}

	for _, cert := range obj.Certificates {
		// Revocations: the physical set must be disjoint from each one.
		for _, rev := range cert.RevocationSlices {
			if !cert.Physical.Intersect(rev).Empty {
				return ErrRevoked
			}
		}
		// Grants: recompute coverage of the physical set by the grant union.
		coveredSize, unionSize, grantProof, ok := CoverUnion(cert.Physical, cert.GrantSlices)
		if !ok || coveredSize != cert.CoveredSize || unionSize != cert.UnionSize {
			return ErrNotCovered
		}
		if len(grantProof.Terms) != len(cert.GrantTerms) {
			return ErrCertificate
		}
		for i, t := range grantProof.Terms {
			claim := cert.GrantTerms[i]
			if !sameInts(t.Sets, claim.Sets) ||
				!t.Aff.Equal(claim.Intersect) ||
				t.Aff.Size() != claim.Cardinality ||
				claim.Sign != signFor(len(t.Sets)) {
				return ErrCertificate
			}
		}
	}
	return nil
}
