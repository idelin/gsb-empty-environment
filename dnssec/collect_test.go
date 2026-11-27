package dnssec

import "testing"

func TestRRSetCollectionHelpers(t *testing.T) {
	host := MustParseName("host.example.")
	other := MustParseName("other.example.")
	rrs := []RR{
		{Name: host, Type: TypeA, Class: ClassIN, TTL: 60, Rdata: []byte{1, 1, 1, 1}},
		{Name: host, Type: TypeA, Class: ClassIN, TTL: 60, Rdata: []byte{2, 2, 2, 2}},
		{Name: host, Type: TypeAAAA, Class: ClassIN, TTL: 60, Rdata: make([]byte, 16)},
		{Name: other, Type: TypeA, Class: ClassIN, TTL: 60, Rdata: []byte{3, 3, 3, 3}},
	}

	aSet := RRSetByNameType(rrs, host, TypeA)
	if len(aSet) != 2 {
		t.Fatalf("A RRset size = %d want 2", len(aSet))
	}
	if got := RRSetByNameType(rrs, host, TypeMX); got != nil {
		t.Fatalf("expected nil for absent type, got %d", len(got))
	}

	// Build an RRSIG covering A and one covering AAAA, plus a foreign owner.
	sigA := RR{Name: host, Type: TypeRRSIG, Class: ClassIN, TTL: 60,
		Rdata: mustRRSIGRdata(t, TypeA)}
	sigAAAA := RR{Name: host, Type: TypeRRSIG, Class: ClassIN, TTL: 60,
		Rdata: mustRRSIGRdata(t, TypeAAAA)}
	sigOther := RR{Name: other, Type: TypeRRSIG, Class: ClassIN, TTL: 60,
		Rdata: mustRRSIGRdata(t, TypeA)}
	all := append(append([]RR{}, rrs...), sigA, sigAAAA, sigOther)

	got := RRSIGsFor(all, host, TypeA)
	if len(got) != 1 {
		t.Fatalf("RRSIGsFor A = %d want 1", len(got))
	}
	parsed, err := ParseRRSIG(got[0].Rdata)
	if err != nil || parsed.TypeCovered != TypeA {
		t.Fatalf("wrong RRSIG selected: %v", err)
	}
	if len(RRSIGsFor(all, host, TypeMX)) != 0 {
		t.Fatal("unexpected RRSIG for uncovered type")
	}

	msg := &Message{Answer: rrs[:2], Authority: rrs[2:], Additional: []RR{sigA}}
	if len(msg.AllRRs()) != 5 {
		t.Fatalf("AllRRs = %d want 5", len(msg.AllRRs()))
	}
}

func mustRRSIGRdata(t *testing.T, covered uint16) []byte {
	t.Helper()
	signer := MustParseName("example.")
	rdata := make([]byte, 0, 18+len(signer.Wire()))
	rdata = appendU16(rdata, covered)
	rdata = append(rdata, AlgECDSAP256SHA256, 3) // algorithm, labels
	rdata = appendU32(rdata, 60)                 // original TTL
	rdata = appendU32(rdata, 2000003600)         // expiration
	rdata = appendU32(rdata, 2000000000)         // inception
	rdata = appendU16(rdata, 12345)              // key tag
	rdata = append(rdata, signer.Wire()...)
	return rdata
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusSecure:   "Secure",
		StatusInsecure: "Insecure",
		StatusBogus:    "Bogus",
		StatusUnknown:  "Unknown",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("%d.String()=%q want %q", s, s.String(), want)
		}
	}
}
