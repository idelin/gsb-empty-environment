package dnssec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"testing"
	"time"
)

type testSigner struct {
	zone Name
	priv *ecdsa.PrivateKey
	key  DNSKEY
	now  time.Time
}

func newTestSigner(t *testing.T, zone string) *testSigner {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := make([]byte, 64)
	priv.X.FillBytes(pub[:32])
	priv.Y.FillBytes(pub[32:])
	return &testSigner{
		zone: MustParseName(zone),
		priv: priv,
		key: DNSKEY{
			Flags:     DNSKEYFlagZoneKey,
			Protocol:  DNSKEYProtocol,
			Algorithm: AlgECDSAP256,
			PublicKey: pub,
		},
		now: time.Unix(1_800_000_000, 0).UTC(),
	}
}

func (s *testSigner) sign(t *testing.T, rrset []RR, labels uint8) RRSIG {
	t.Helper()
	sig := RRSIG{
		TypeCovered: rrset[0].Type,
		Algorithm:   AlgECDSAP256,
		Labels:      labels,
		OriginalTTL: rrset[0].TTL,
		Inception:   uint32(s.now.Add(-time.Hour).Unix()),
		Expiration:  uint32(s.now.Add(24 * time.Hour).Unix()),
		KeyTag:      s.key.KeyTag(),
		SignerName:  s.zone,
	}
	data, err := DataToSign(rrset, sig)
	if err != nil {
		t.Fatalf("DataToSign: %v", err)
	}
	digest := sha256.Sum256(data)
	r, sv, err := ecdsa.Sign(rand.Reader, s.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sv.FillBytes(raw[32:])
	sig.Signature = raw
	return sig
}

type testNSEC3Name struct {
	name  Name
	types []uint16
	flags []uint8
}

func (s *testSigner) buildNSEC3Chain(t *testing.T, salt []byte, iterations uint16, names []testNSEC3Name) ([]NSEC3Record, []SignedNSEC3) {
	t.Helper()
	type hashed struct {
		hash  []byte
		types []uint16
		flags uint8
	}
	var entries []hashed
	for _, n := range names {
		hash, err := HashNSEC3(n.name, salt, iterations)
		if err != nil {
			t.Fatal(err)
		}
		flags := uint8(0)
		if len(n.flags) > 0 {
			flags = n.flags[0]
		}
		entries = append(entries, hashed{hash: hash, types: n.types, flags: flags})
	}
	sort.Slice(entries, func(i, j int) bool {
		return string(entries[i].hash) < string(entries[j].hash)
	})

	records := make([]NSEC3Record, 0, len(entries))
	signed := make([]SignedNSEC3, 0, len(entries))
	for i, entry := range entries {
		next := entries[(i+1)%len(entries)].hash
		n := NSEC3{
			HashAlgorithm: NSEC3HashSHA1,
			Flags:         entry.flags,
			Iterations:    iterations,
			Salt:          append([]byte(nil), salt...),
			NextHash:      append([]byte(nil), next...),
			Types:         append([]uint16(nil), entry.types...),
		}
		owner := NSEC3Owner(s.zone, entry.hash)
		rec := NSEC3Record{Owner: owner, Class: ClassINET, TTL: 300, NSEC3: n}
		rrset := []RR{rec.RR()}
		sig := s.sign(t, rrset, uint8(owner.LabelCount()))
		records = append(records, rec)
		signed = append(signed, SignedNSEC3{Record: rec, Signatures: []RRSIG{sig}})
	}
	return records, signed
}

func nsec3Types(types ...uint16) []uint16 { return types }

func makeA(data uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, data)
	return out
}
