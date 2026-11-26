package dnssec

import "testing"

func normalTestZone(t *testing.T) (*testSigner, *NSEC3Set, []NSEC3Record, []SignedNSEC3) {
	t.Helper()
	signer := newTestSigner(t, "normal.test.")
	names := []testNSEC3Name{
		{name: signer.zone, types: nsec3Types(TypeSOA, TypeNS, TypeRRSIG, TypeNSEC3)},
		{name: MustParseName("www.normal.test."), types: nsec3Types(TypeA, TypeRRSIG)},
		{name: MustParseName("mail.normal.test."), types: nsec3Types(TypeMX, TypeRRSIG)},
		{name: MustParseName("alias.normal.test."), types: nsec3Types(TypeCNAME, TypeRRSIG)},
		{name: MustParseName("ent.normal.test."), types: nil},
	}
	records, signed := signer.buildNSEC3Chain(t, []byte{0xaa, 0xbb}, 5, names)
	set, err := NewNSEC3Set(signer.zone, records, DefaultMaxIterations)
	if err != nil {
		t.Fatal(err)
	}
	return signer, set, records, signed
}

func resignNSEC3(t *testing.T, signer *testSigner, rec NSEC3Record) SignedNSEC3 {
	t.Helper()
	rrset := []RR{rec.RR()}
	return SignedNSEC3{Record: rec, Signatures: []RRSIG{signer.sign(t, rrset, uint8(rec.Owner.LabelCount()))}}
}

func TestNXDOMAINThreeLinkProof(t *testing.T) {
	signer, set, _, signed := normalTestZone(t)
	qname := MustParseName("missing.normal.test.")
	proof, err := ValidateNXDOMAIN(set, qname)
	if err != nil {
		t.Fatalf("valid NXDOMAIN rejected: %v", err)
	}
	if !EqualNames(proof.ClosestEncloser, signer.zone) {
		t.Fatalf("closest encloser = %s", proof.ClosestEncloser)
	}
	if !EqualNames(proof.NextCloser, qname) {
		t.Fatalf("next closer = %s", proof.NextCloser)
	}
	if !EqualNames(proof.Wildcard, signer.zone.Wildcard()) {
		t.Fatalf("wildcard = %s", proof.Wildcard)
	}
	if proof.ClosestEncloserRR == nil || proof.NextCloserRR == nil || proof.WildcardCoverageRR == nil {
		t.Fatal("incomplete three-link proof")
	}

	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.NXDOMAIN(qname, signed); err != nil {
		t.Fatalf("signed NXDOMAIN evidence rejected: %v", err)
	}
}

func TestNXDOMAINUsesNextCloserNotQName(t *testing.T) {
	_, set, _, _ := normalTestZone(t)
	qname := MustParseName("deep.foo.normal.test.")
	proof, err := ValidateNXDOMAIN(set, qname)
	if err != nil {
		t.Fatalf("valid multi-label NXDOMAIN rejected: %v", err)
	}
	want := MustParseName("foo.normal.test.")
	if !EqualNames(proof.NextCloser, want) {
		t.Fatalf("next closer = %s, want %s", proof.NextCloser, want)
	}
}

func TestNXDOMAINRejectsExistingExactOwner(t *testing.T) {
	_, set, _, _ := normalTestZone(t)
	qname := MustParseName("www.normal.test.")
	if _, err := ValidateNXDOMAIN(set, qname); err == nil {
		t.Fatal("NXDOMAIN accepted for an existing exact owner")
	}
}

func TestNXDOMAINRejectsOptOutNextCloser(t *testing.T) {
	signer, set, records, signed := normalTestZone(t)
	qname := MustParseName("missing.normal.test.")
	proof, err := ValidateNXDOMAIN(set, qname)
	if err != nil {
		t.Fatal(err)
	}
	for i := range records {
		if EqualNames(records[i].Owner, proof.NextCloserRR.Owner) {
			records[i].NSEC3.Flags |= NSEC3FlagOptOut
			signed[i] = resignNSEC3(t, signer, records[i])
			break
		}
	}
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.NXDOMAIN(qname, signed); err == nil {
		t.Fatal("NXDOMAIN accepted Opt-Out next-closer evidence")
	}
}

func TestNXDOMAINRejectsMissingWildcardLink(t *testing.T) {
	signer, set, _, signed := normalTestZone(t)
	qname := MustParseName("missing.normal.test.")
	proof, err := ValidateNXDOMAIN(set, qname)
	if err != nil {
		t.Fatal(err)
	}
	// Keep only the closest-encloser and next-closer records. If those two
	// do not also cover the wildcard, the third link is absent.
	recs := []NSEC3Record{*proof.ClosestEncloserRR, *proof.NextCloserRR}
	subset, err := NewNSEC3Set(set.Zone, recs, DefaultMaxIterations)
	if err != nil {
		t.Fatalf("subset set: %v", err)
	}
	_, _, wildcardCovered, err := subset.FindCoverageName(proof.Wildcard)
	if err != nil {
		t.Fatal(err)
	}
	if wildcardCovered {
		t.Skip("the two selected intervals also cover the wildcard; link sharing is legitimate")
	}
	if _, err := ValidateNXDOMAIN(subset, qname); err == nil {
		t.Fatal("NXDOMAIN accepted without wildcard coverage")
	}

	// Re-authenticate the same subset through the signature path.
	var subsetSigned []SignedNSEC3
	for _, want := range recs {
		for _, evidence := range signed {
			if EqualNames(evidence.Record.Owner, want.Owner) {
				subsetSigned = append(subsetSigned, evidence)
				break
			}
		}
	}
	if len(subsetSigned) != len(recs) {
		t.Fatal("failed to assemble signed subset")
	}
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.NXDOMAIN(qname, subsetSigned); err == nil {
		t.Fatal("signed NXDOMAIN accepted without wildcard coverage")
	}
}

func TestNODATAExactENTAndCNAMERules(t *testing.T) {
	signer, set, _, signed := normalTestZone(t)

	proof, err := ValidateNODATA(set, MustParseName("www.normal.test."), TypeAAAA)
	if err != nil {
		t.Fatalf("valid exact NODATA rejected: %v", err)
	}
	if proof.Kind != NODATAExact {
		t.Fatalf("kind = %v", proof.Kind)
	}
	if _, err := ValidateNODATA(set, MustParseName("www.normal.test."), TypeA); err == nil {
		t.Fatal("NODATA accepted for an existing A type")
	}

	proof, err = ValidateNODATA(set, MustParseName("ent.normal.test."), TypeA)
	if err != nil {
		t.Fatalf("ENT NODATA rejected: %v", err)
	}
	if proof.Kind != NODATAEmptyNonTerminal {
		t.Fatalf("ENT kind = %v", proof.Kind)
	}

	if _, err := ValidateNODATA(set, MustParseName("alias.normal.test."), TypeA); err == nil {
		t.Fatal("NODATA ignored the presence of CNAME")
	}
	if _, err := ValidateNODATA(set, MustParseName("alias.normal.test."), TypeCNAME); err == nil {
		t.Fatal("NODATA accepted for an existing CNAME type")
	}
	if _, err := ValidateNODATA(set, MustParseName("www.normal.test."), TypeCNAME); err != nil {
		t.Fatalf("CNAME absence should permit CNAME NODATA: %v", err)
	}
	if _, err := ValidateNODATA(set, MustParseName("missing.normal.test."), TypeA); err == nil {
		t.Fatal("ordinary non-existence coverage was accepted as NODATA")
	}

	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.NODATA(MustParseName("www.normal.test."), TypeAAAA, signed); err != nil {
		t.Fatalf("signed NODATA evidence rejected: %v", err)
	}
	if _, err := validator.NODATA(MustParseName("ent.normal.test."), TypeA, signed); err != nil {
		t.Fatalf("signed ENT NODATA evidence rejected: %v", err)
	}
	if _, err := validator.NODATA(MustParseName("alias.normal.test."), TypeA, signed); err == nil {
		t.Fatal("signed NODATA ignored CNAME presence")
	}
}

func TestOptOutUnsignedDelegationBoundary(t *testing.T) {
	signer := newTestSigner(t, "optout.test.")
	unsigned := MustParseName("unsigned.optout.test.")
	names := []testNSEC3Name{
		{name: signer.zone, types: nsec3Types(TypeSOA, TypeNS)},
		{name: MustParseName("a.optout.test."), types: nsec3Types(TypeA)},
		{name: MustParseName("b.optout.test."), types: nsec3Types(TypeA)},
	}
	records, signed := signer.buildNSEC3Chain(t, []byte{0x01}, 7, names)
	set, err := NewNSEC3Set(signer.zone, records, DefaultMaxIterations)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := set.Hash(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	cover, ok := set.FindCoverage(hash)
	if !ok {
		t.Fatal("unsigned delegation name is unexpectedly not covered")
	}
	for i := range records {
		if EqualNames(records[i].Owner, cover.Owner) {
			records[i].NSEC3.Flags |= NSEC3FlagOptOut
			signed[i] = resignNSEC3(t, signer, records[i])
			break
		}
	}
	set, err = NewNSEC3Set(signer.zone, records, DefaultMaxIterations)
	if err != nil {
		t.Fatal(err)
	}

	proof, err := ValidateNODATA(set, unsigned, TypeDS, true)
	if err != nil {
		t.Fatalf("Opt-Out DS NODATA rejected: %v", err)
	}
	if proof.Kind != NODATAOptOutDelegation || !proof.OptOut {
		t.Fatalf("unexpected proof: %+v", proof)
	}
	if _, err := ValidateNODATA(set, unsigned, TypeA, true); err == nil {
		t.Fatal("Opt-Out accepted for a non-DS type at a delegation")
	}
	if _, err := ValidateNODATA(set, unsigned, TypeDS, false); err == nil {
		t.Fatal("Opt-Out accepted for a non-delegation query context")
	}
	if _, err := ValidateNODATA(set, MustParseName("not-a-delegation.optout.test."), TypeDS, false); err == nil {
		t.Fatal("Opt-Out accepted for a non-delegation name")
	}

	validator := NewValidatorWithLimit(signer.zone, []DNSKEY{signer.key}, signer.now, 250)
	if _, err := validator.NODATA(unsigned, TypeDS, signed, true); err != nil {
		t.Fatalf("signed Opt-Out DS evidence rejected: %v", err)
	}
	if _, err := validator.NODATA(unsigned, TypeDS, signed, false); err == nil {
		t.Fatal("signed Opt-Out evidence accepted without delegation context")
	}
}

func wildcardTestZone(t *testing.T) (*testSigner, *NSEC3Set, []SignedNSEC3) {
	t.Helper()
	signer := newTestSigner(t, "wild.test.")
	names := []testNSEC3Name{
		{name: signer.zone, types: nsec3Types(TypeSOA, TypeNS)},
		{name: signer.zone.Wildcard(), types: nsec3Types(TypeA, TypeRRSIG)},
		{name: MustParseName("existing.wild.test."), types: nsec3Types(TypeA, TypeRRSIG)},
		{name: MustParseName("other.wild.test."), types: nsec3Types(TypeTXT)},
	}
	records, signed := signer.buildNSEC3Chain(t, []byte{0xcc}, 3, names)
	set, err := NewNSEC3Set(signer.zone, records, DefaultMaxIterations)
	if err != nil {
		t.Fatal(err)
	}
	return signer, set, signed
}

func TestWildcardAnswerRequiresNextCloserDenial(t *testing.T) {
	signer, set, signed := wildcardTestZone(t)
	qname := MustParseName("anything.wild.test.")
	proof, err := ValidateWildcardAnswer(set, qname, uint8(signer.zone.LabelCount()))
	if err != nil {
		t.Fatalf("valid wildcard proof rejected: %v", err)
	}
	if !EqualNames(proof.Wildcard, MustParseName("*.wild.test.")) {
		t.Fatalf("wildcard = %s", proof.Wildcard)
	}
	if !EqualNames(proof.NextCloser, qname) {
		t.Fatalf("next closer = %s", proof.NextCloser)
	}

	rrset := []RR{{Name: qname, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(7)}}
	sig := signer.sign(t, rrset, uint8(signer.zone.LabelCount()))
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.WildcardAnswer(qname, rrset, []RRSIG{sig}, signed); err != nil {
		t.Fatalf("signed wildcard answer rejected: %v", err)
	}
}

func TestNXDOMAINRejectsExistingWildcard(t *testing.T) {
	signer, set, signed := wildcardTestZone(t)
	qname := MustParseName("missing.wild.test.")
	if _, err := ValidateNXDOMAIN(set, qname); err == nil {
		t.Fatal("NXDOMAIN accepted while the closest encloser's wildcard exists")
	}
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.NXDOMAIN(qname, signed); err == nil {
		t.Fatal("signed NXDOMAIN accepted while the closest encloser's wildcard exists")
	}
}

func TestWildcardCannotShadowExactOwner(t *testing.T) {
	signer, set, signed := wildcardTestZone(t)
	qname := MustParseName("existing.wild.test.")

	if _, err := ValidateWildcardAnswer(set, qname, uint8(signer.zone.LabelCount())); err == nil {
		t.Fatal("wildcard proof shadowed an exact owner")
	}

	rrset := []RR{{Name: qname, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(1)}}
	sig := signer.sign(t, rrset, uint8(signer.zone.LabelCount()))
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.WildcardAnswer(qname, rrset, []RRSIG{sig}, signed); err == nil {
		t.Fatal("valid replayed wildcard signature shadowed the real exact owner")
	}
}

func TestWildcardRejectsOptOutNextCloser(t *testing.T) {
	signer, set, signed := wildcardTestZone(t)
	qname := MustParseName("anything.wild.test.")
	proof, err := ValidateWildcardAnswer(set, qname, uint8(signer.zone.LabelCount()))
	if err != nil {
		t.Fatal(err)
	}
	for i := range signed {
		if EqualNames(signed[i].Record.Owner, proof.NextCloserRR.Owner) {
			rec := signed[i].Record
			rec.NSEC3.Flags |= NSEC3FlagOptOut
			signed[i] = resignNSEC3(t, signer, rec)
			break
		}
	}
	rrset := []RR{{Name: qname, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(7)}}
	sig := signer.sign(t, rrset, uint8(signer.zone.LabelCount()))
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.WildcardAnswer(qname, rrset, []RRSIG{sig}, signed); err == nil {
		t.Fatal("wildcard answer accepted Opt-Out next-closer coverage")
	}
}

func TestAuthenticationRejectsTamperedNSEC3(t *testing.T) {
	signer, _, _, signed := normalTestZone(t)
	signed[0].Record.NSEC3.Iterations++
	validator := NewValidator(signer.zone, []DNSKEY{signer.key}, signer.now)
	if _, err := validator.AuthenticateNSEC3(signed); err == nil {
		t.Fatal("tampered NSEC3 RDATA passed signature authentication")
	}
	signed[0].Record.NSEC3.Iterations--

	badsig := signed
	badsig[0].Signatures = []RRSIG{badsig[0].Signatures[0]}
	badsig[0].Signatures[0].Labels--
	if _, err := validator.AuthenticateNSEC3(badsig); err == nil {
		t.Fatal("wildcard-indicated NSEC3 RRSIG labels were accepted")
	}
}

func TestAuthenticationRejectsOutOfZoneSigner(t *testing.T) {
	_, _, _, signed := normalTestZone(t)
	parent := newTestSigner(t, "test.")
	rec := signed[0].Record
	rrset := []RR{rec.RR()}
	parentSig := parent.sign(t, rrset, uint8(rec.Owner.LabelCount()))
	evidence := []SignedNSEC3{{Record: rec, Signatures: []RRSIG{parentSig}}}

	validator := NewValidator(MustParseName("normal.test."), []DNSKEY{parent.key}, parent.now)
	if _, err := validator.AuthenticateNSEC3(evidence); err == nil {
		t.Fatal("NSEC3 RRSIG from the parent zone was accepted as current-zone evidence")
	}
}
