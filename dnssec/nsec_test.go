package dnssec

import "testing"

func TestNSECDenialAndWildcardOcclusion(t *testing.T) {
	priv, keyRR := mustKey(t)
	zone := MustParseName("example.")
	keyRR.Name = zone

	// NSEC chain ordered canonically:
	// example. -> a.example. -> m.example. -> www.example. -> example.
	apex := nsecRR(zone, MustParseName("a.example."),
		[]uint16{TypeSOA, TypeNS, TypeDNSKEY, TypeRRSIG})
	a := nsecRR(MustParseName("a.example."), MustParseName("m.example."),
		[]uint16{TypeA, TypeRRSIG})
	m := nsecRR(MustParseName("m.example."), MustParseName("www.example."),
		[]uint16{TypeA, TypeRRSIG})
	www := nsecRR(MustParseName("www.example."), zone,
		[]uint16{TypeA, TypeRRSIG})

	sign := func(rr RR) RR {
		return signRRset(t, []RR{rr}, zone, priv, keyRR,
			uint8(rr.Name.LabelCount()), 2000000000, 2000003600)
	}
	nsecs := []RR{apex, a, m, www}
	sigs := []RR{sign(apex), sign(a), sign(m), sign(www)}

	v := &Validator{Now: fixedTime(2000001000)}

	// NXDOMAIN for n.example. falls strictly between m and www (coverage)
	// and requires the wildcard proof.
	qname := MustParseName("n.example.")
	res, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: zone,
		NSEC: nsecs, NSECSig: sigs, ZoneKeys: []RR{keyRR},
	})
	if err != nil {
		t.Fatalf("NSEC NXDOMAIN err=%v", err)
	}
	if res.Status != StatusSecure {
		t.Fatalf("NSEC NXDOMAIN: status=%v", res.Status)
	}

	// NODATA for www.example. / AAAA: exact match, bitmap lacks AAAA/CNAME.
	res2, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNODATA, Qname: MustParseName("www.example."),
		Qtype: TypeAAAA, Zone: zone, NSEC: nsecs, NSECSig: sigs,
		ZoneKeys: []RR{keyRR},
	})
	if err != nil || res2.Status != StatusSecure {
		t.Fatalf("NSEC NODATA: status=%v err=%v", res2.Status, err)
	}

	// Wildcard answer occlusion check: a wildcard-synthesized answer for
	// zzz.example. must be backed by NSEC coverage of that exact name.
	proof, err := v.VerifyDenial(&DenialInput{
		Kind: DenialWildcardAnswer, Qname: qname, Qtype: TypeA, Zone: zone,
		NSEC: nsecs, NSECSig: sigs, ZoneKeys: []RR{keyRR},
	})
	if err != nil {
		t.Fatalf("wildcard NSEC proof: %v", err)
	}
	owner := qname
	rrset := []RR{{Name: owner, Type: TypeA, Class: ClassIN, TTL: testTTL,
		Rdata: []byte{10, 0, 0, 1}}}
	wsig := signRRset(t, rrset, zone, priv, keyRR, 2, 2000000000, 2000003600)
	if _, err := v.VerifyRRSetWithWildcard(rrset, []RR{wsig}, []RR{keyRR}, owner, proof); err != nil {
		t.Fatalf("wildcard answer with NSEC occlusion proof: %v", err)
	}

	// Unsigned NSEC records must be Bogus.
	if _, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: zone,
		NSEC: nsecs, NSECSig: nil, ZoneKeys: []RR{keyRR},
	}); !IsBogus(err) {
		t.Fatal("unsigned NSEC accepted")
	}
}
