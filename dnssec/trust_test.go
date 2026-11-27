package dnssec

import "testing"

func TestDSDigestAndTrustChain(t *testing.T) {
	priv, keyRR := mustKey(t)
	child := MustParseName("child.example.")
	keyRR.Name = child
	key, err := ParseDNSKEY(keyRR.Rdata)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// SHA-256 DS record as the parent would publish.
	digest256, err := ComputeDSDigest(child, key, DigestSHA256)
	if err != nil {
		t.Fatalf("sha256 digest: %v", err)
	}
	dsRdata := make([]byte, 0, 4+len(digest256))
	dsRdata = appendU16(dsRdata, DNSKEYKeyTag(key))
	dsRdata = append(dsRdata, key.Algorithm, DigestSHA256)
	dsRdata = append(dsRdata, digest256...)
	dsRR := RR{Name: child, Type: TypeDS, Class: ClassIN, TTL: testTTL, Rdata: dsRdata}

	if err := VerifyDNSKEYByDS(dsRR, child, keyRR); err != nil {
		t.Fatalf("DS->DNSKEY SHA-256 chain failed: %v", err)
	}

	// SHA-1 digest type must validate as well (real SHA-1 primitive).
	digest1, err := ComputeDSDigest(child, key, DigestSHA1)
	if err != nil {
		t.Fatalf("sha1 digest: %v", err)
	}
	if len(digest1) != 20 {
		t.Fatalf("SHA-1 digest length = %d want 20", len(digest1))
	}
	ds1 := make([]byte, 0, 4+len(digest1))
	ds1 = appendU16(ds1, DNSKEYKeyTag(key))
	ds1 = append(ds1, key.Algorithm, DigestSHA1)
	ds1 = append(ds1, digest1...)
	ds1RR := RR{Name: child, Type: TypeDS, Class: ClassIN, TTL: testTTL, Rdata: ds1}
	if err := VerifyDNSKEYByDS(ds1RR, child, keyRR); err != nil {
		t.Fatalf("DS->DNSKEY SHA-1 chain failed: %v", err)
	}

	// Corrupt the digest -> Bogus.
	badDS := RR{Name: child, Type: TypeDS, Class: ClassIN, TTL: testTTL, Rdata: append([]byte(nil), dsRdata...)}
	badDS.Rdata[len(badDS.Rdata)-1] ^= 0xFF
	if err := VerifyDNSKEYByDS(badDS, child, keyRR); !IsBogus(err) {
		t.Fatalf("corrupted DS accepted: %v", err)
	}

	// Wrong owner name yields a different digest -> Bogus.
	if err := VerifyDNSKEYByDS(dsRR, MustParseName("other.example."), keyRR); !IsBogus(err) {
		t.Fatalf("DS validated against wrong owner: %v", err)
	}

	// A revoked DNSKEY must not be anchored.
	revoked := RR{Name: child, Type: TypeDNSKEY, Class: ClassIN, TTL: testTTL, Rdata: append([]byte(nil), keyRR.Rdata...)}
	revoked.Rdata[1] = 0x80
	if err := VerifyDNSKEYByDS(dsRR, child, revoked); !IsBogus(err) {
		t.Fatalf("revoked key anchored by DS: %v", err)
	}

	// Keep priv referenced: the full signed chain test below reuses it.
	_ = priv
}

func TestKeyTagStable(t *testing.T) {
	_, keyRR := mustKey(t)
	key, _ := ParseDNSKEY(keyRR.Rdata)
	tag1 := DNSKEYKeyTag(key)
	tag2 := KeyTag(key.Flags, key.Protocol, key.Algorithm, key.PublicKey)
	if tag1 != tag2 || tag1 == 0 {
		t.Fatalf("inconsistent key tag: %d vs %d", tag1, tag2)
	}
}
