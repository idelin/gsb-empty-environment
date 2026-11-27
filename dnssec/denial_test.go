package dnssec

import (
	"crypto/ecdsa"
	"testing"
	"time"
)

type nsec3Chain struct {
	zone     Name
	salt     []byte
	iter     uint16
	keyRR    RR
	priv     *ecdsa.PrivateKey
	records  map[string]RR
	sigs     map[string]RR
	www      Name
	ns       Name
	apexHash []byte
	wwwHash  []byte
	nsHash   []byte
}

func buildNSEC3Chain(t *testing.T, optOut bool) *nsec3Chain {
	t.Helper()
	priv, keyRR := mustKey(t)
	zone := MustParseName("example.")
	keyRR.Name = zone

	c := &nsec3Chain{
		zone:    zone,
		salt:    []byte{0x01, 0x02},
		iter:    2,
		keyRR:   keyRR,
		priv:    priv,
		records: map[string]RR{},
		sigs:    map[string]RR{},
		www:     MustParseName("www.example."),
		ns:      MustParseName("ns.example."),
	}

	hash := func(n Name) []byte {
		h, err := NSEC3Hash(n, c.salt, c.iter, NSEC3SHA1)
		if err != nil {
			t.Fatalf("hash %s: %v", n, err)
		}
		return h
	}
	c.apexHash = hash(zone)
	c.wwwHash = hash(c.www)
	c.nsHash = hash(c.ns)

	type node struct {
		name Name
		h    []byte
	}
	nodes := []node{{zone, c.apexHash}, {c.www, c.wwwHash}, {c.ns, c.nsHash}}
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && compareBytes(nodes[j-1].h, nodes[j].h) > 0; j-- {
			nodes[j-1], nodes[j] = nodes[j], nodes[j-1]
		}
	}

	flags := uint8(0)
	if optOut {
		flags = NSEC3OptOut
	}

	for i, cur := range nodes {
		nxt := nodes[(i+1)%len(nodes)]
		types := []uint16{TypeRRSIG, TypeNSEC3PARAM, TypeSOA, TypeDNSKEY}
		if cur.name.Equal(c.www) {
			types = []uint16{TypeA, TypeRRSIG}
		}
		if cur.name.Equal(c.ns) {
			types = []uint16{TypeNS}
		}
		rr := nsec3RR(cur.h, nxt.h, c.salt, c.iter, flags, types, zone)
		c.records[rr.Name.String()] = rr
	}

	for k, rr := range c.records {
		sig := signRRset(t, []RR{rr}, zone, priv, keyRR,
			uint8(zone.LabelCount()), 2000000000, 2000003600)
		c.sigs[k] = sig
	}
	return c
}

func (c *nsec3Chain) slices(skip ...string) (rrs, sigs []RR) {
skipLoop:
	for k, rr := range c.records {
		for _, s := range skip {
			if k == s {
				continue skipLoop
			}
		}
		rrs = append(rrs, rr)
		sigs = append(sigs, c.sigs[k])
	}
	return rrs, sigs
}

func (c *nsec3Chain) validator() *Validator {
	return &Validator{Now: fixedTime(2000001000), Skew: time.Second}
}

func mustHash(t *testing.T, n Name, salt []byte, iter uint16) []byte {
	t.Helper()
	h, err := NSEC3Hash(n, salt, iter, NSEC3SHA1)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

func nsec3OwnerForHash(t *testing.T, h []byte, zone Name) string {
	t.Helper()
	n := NameFromLabels(append([]string{Base32HexEncode(h)}, labelsToStrings(zone)...)...)
	return n.String()
}

func ownerCovering(t *testing.T, c *nsec3Chain, target []byte) string {
	t.Helper()
	for _, rr := range c.records {
		n3, err := ParseNSEC3(rr.Rdata)
		if err != nil {
			t.Fatalf("parse nsec3: %v", err)
		}
		h, err := HashedOwnerName(rr, c.zone)
		if err != nil {
			t.Fatalf("owner: %v", err)
		}
		if NSEC3Covers(h, n3.NextHashed, target) {
			return rr.Name.String()
		}
	}
	t.Fatalf("no record covers %x", target)
	return ""
}

func TestNXDOMAINTripleProof(t *testing.T) {
	c := buildNSEC3Chain(t, false)
	qname := MustParseName("missing.www.example.")
	nsec3, sigs := c.slices()

	v := c.validator()
	res, err := v.VerifyDenial(&DenialInput{
		Kind:     DenialNXDOMAIN,
		Qname:    qname,
		Qtype:    TypeA,
		Zone:     c.zone,
		NSEC3:    nsec3,
		NSEC3Sig: sigs,
		ZoneKeys: []RR{c.keyRR},
	})
	if err != nil || res.Status != StatusSecure {
		t.Fatalf("triple proof: status=%v err=%v", res.Status, err)
	}
	if !res.ClosestEncloser.Equal(c.www) {
		t.Errorf("closest encloser = %s want %s", res.ClosestEncloser, c.www)
	}
	if res.NextCloser.String() != "missing.www.example." {
		t.Errorf("next closer = %s", res.NextCloser)
	}
	if res.Wildcard.String() != "*.www.example." {
		t.Errorf("wildcard = %s", res.Wildcard)
	}

	// Remove (a) the closest-encloser record -> Bogus.
	wwwOwner := nsec3OwnerForHash(t, c.wwwHash, c.zone)
	r, s := c.slices(wwwOwner)
	if _, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: c.zone,
		NSEC3: r, NSEC3Sig: s, ZoneKeys: []RR{c.keyRR},
	}); !IsBogus(err) {
		t.Fatalf("missing closest-encloser proof accepted: %v", err)
	}

	// Remove (b) the record covering the next closer name -> Bogus.
	ncHash := mustHash(t, MustParseName("missing.www.example."), c.salt, c.iter)
	ncOwner := ownerCovering(t, c, ncHash)
	r, s = c.slices(ncOwner)
	if _, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: c.zone,
		NSEC3: r, NSEC3Sig: s, ZoneKeys: []RR{c.keyRR},
	}); !IsBogus(err) {
		t.Fatalf("missing next-closer coverage accepted: %v", err)
	}

	// Remove (c) the record covering the wildcard hash -> Bogus.
	wildHash := mustHash(t, MustParseName("*.www.example."), c.salt, c.iter)
	wildOwner := ownerCovering(t, c, wildHash)
	r2, s2 := c.slices(wildOwner)
	if _, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: c.zone,
		NSEC3: r2, NSEC3Sig: s2, ZoneKeys: []RR{c.keyRR},
	}); !IsBogus(err) {
		t.Fatalf("missing wildcard coverage accepted: %v", err)
	}
}

func TestNSEC3WraparoundIntervalNXDOMAIN(t *testing.T) {
	c := buildNSEC3Chain(t, false)

	foundWrap := false
	for _, rr := range c.records {
		n3, _ := ParseNSEC3(rr.Rdata)
		h, _ := HashedOwnerName(rr, c.zone)
		if compareBytes(h, n3.NextHashed) > 0 {
			foundWrap = true
		}
	}
	if !foundWrap {
		t.Fatal("test chain contains no wraparound interval")
	}

	qname := MustParseName("wrapcheck.example.")
	nsec3, sigs := c.slices()
	res, err := c.validator().VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: c.zone,
		NSEC3: nsec3, NSEC3Sig: sigs, ZoneKeys: []RR{c.keyRR},
	})
	if err != nil || res.Status != StatusSecure {
		t.Fatalf("wraparound NXDOMAIN: status=%v err=%v", res.Status, err)
	}
}

func TestNODATABitmapChecks(t *testing.T) {
	c := buildNSEC3Chain(t, false)
	nsec3, sigs := c.slices()
	v := c.validator()

	res, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNODATA, Qname: c.www, Qtype: TypeAAAA, Zone: c.zone,
		NSEC3: nsec3, NSEC3Sig: sigs, ZoneKeys: []RR{c.keyRR},
	})
	if err != nil || res.Status != StatusSecure {
		t.Fatalf("NODATA: status=%v err=%v", res.Status, err)
	}

	if _, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNODATA, Qname: c.www, Qtype: TypeA, Zone: c.zone,
		NSEC3: nsec3, NSEC3Sig: sigs, ZoneKeys: []RR{c.keyRR},
	}); !IsBogus(err) {
		t.Fatal("NODATA accepted for a type present in the bitmap")
	}

	// A CNAME in the bitmap must also force Bogus. Take the signed www
	// record and re-encode its bitmap to include CNAME; its RRSIG no longer
	// matches, so the denial must be rejected.
	wwwOwner := nsec3OwnerForHash(t, c.wwwHash, c.zone)
	original := c.records[wwwOwner]
	n3, err := ParseNSEC3(original.Rdata)
	if err != nil {
		t.Fatalf("parse www nsec3: %v", err)
	}
	tampered := nsec3RR(c.wwwHash, n3.NextHashed, c.salt, c.iter, n3.Flags,
		[]uint16{TypeA, TypeCNAME, TypeRRSIG}, c.zone)
	var all, allSig []RR
	for _, rr := range nsec3 {
		if rr.Name.String() == wwwOwner {
			all = append(all, tampered)
		} else {
			all = append(all, rr)
		}
	}
	allSig = append(allSig, sigs...)
	if _, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNODATA, Qname: c.www,
		Qtype: TypeA, Zone: c.zone, NSEC3: all, NSEC3Sig: allSig,
		ZoneKeys: []RR{c.keyRR},
	}); !IsBogus(err) {
		t.Fatal("NODATA accepted despite CNAME in bitmap / broken signature")
	}
}

func TestOptOutInsecureDelegation(t *testing.T) {
	c := buildNSEC3Chain(t, true)
	qname := MustParseName("delegated.ns.example.")
	nsec3, sigs := c.slices()
	v := c.validator()

	res, err := v.VerifyDenial(&DenialInput{
		Kind: DenialNODATA, Qname: qname, Qtype: TypeDS, Zone: c.zone,
		NSEC3: nsec3, NSEC3Sig: sigs, ZoneKeys: []RR{c.keyRR},
	})
	if err != nil {
		t.Fatalf("opt-out proof errored: %v", err)
	}
	if res.Status != StatusInsecure || !res.OptOut {
		t.Fatalf("opt-out: status=%v optOut=%v want Insecure/true", res.Status, res.OptOut)
	}

	resA, errA := v.VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: qname, Qtype: TypeA, Zone: c.zone,
		NSEC3: nsec3, NSEC3Sig: sigs, ZoneKeys: []RR{c.keyRR},
	})
	if errA != nil || resA.Status != StatusSecure {
		t.Fatalf("opt-out NXDOMAIN for A: status=%v err=%v", resA.Status, errA)
	}
}

func TestUnsignedNSEC3IsBogus(t *testing.T) {
	c := buildNSEC3Chain(t, false)
	nsec3, _ := c.slices()
	if _, err := c.validator().VerifyDenial(&DenialInput{
		Kind: DenialNXDOMAIN, Qname: MustParseName("x.www.example."), Qtype: TypeA,
		Zone: c.zone, NSEC3: nsec3, NSEC3Sig: nil, ZoneKeys: []RR{c.keyRR},
	}); !IsBogus(err) {
		t.Fatalf("unsigned NSEC3 accepted: %v", err)
	}
}

func TestWildcardAnswerNoCloserProof(t *testing.T) {
	c := buildNSEC3Chain(t, false)
	priv, keyRR := mustKey(t)
	zone := c.zone
	keyRR.Name = zone

	owner := MustParseName("anything.www.example.")
	rrset := []RR{{Name: owner, Type: TypeA, Class: ClassIN, TTL: testTTL,
		Rdata: []byte{192, 0, 2, 55}}}
	// Labels=3 => synthesized from the 3-label suffix wildcard *.www.example.
	sig := signRRset(t, rrset, zone, priv, keyRR, 3, 2000000000, 2000003600)

	nsec3, sigs := c.slices()
	v := c.validator()
	proof, err := v.VerifyDenial(&DenialInput{
		Kind: DenialWildcardAnswer, Qname: owner, Qtype: TypeA, Zone: zone,
		NSEC3: nsec3, NSEC3Sig: sigs, ZoneKeys: []RR{c.keyRR},
	})
	if err != nil || proof.Status != StatusSecure {
		t.Fatalf("wildcard denial: status=%v err=%v", proof.Status, err)
	}

	res, err := v.VerifyRRSetWithWildcard(rrset, []RR{sig}, []RR{keyRR}, owner, proof)
	if err != nil || res.Status != StatusSecure {
		t.Fatalf("wildcard RRset: status=%v err=%v", res.Status, err)
	}
}
