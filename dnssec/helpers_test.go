package dnssec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

const testTTL = 3600

func mustKey(t *testing.T) (*ecdsa.PrivateKey, RR) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := elliptic.Marshal(priv.Curve, priv.X, priv.Y) // 65 octets, 0x04 prefix
	// RFC 6605 ECDSA public keys omit the uncompressed-point prefix.
	pub = pub[1:]
	rdata := append([]byte{0x01, 0x00, 0x03, AlgECDSAP256SHA256}, pub...)
	rr := RR{
		Name:  MustParseName("example."),
		Type:  TypeDNSKEY,
		Class: ClassIN,
		TTL:   testTTL,
		Rdata: rdata,
	}
	return priv, rr
}

func keyRR(t *testing.T, owner Name, priv *ecdsa.PrivateKey) RR {
	t.Helper()
	pub := elliptic.Marshal(priv.Curve, priv.X, priv.Y)[1:]
	rdata := append([]byte{0x01, 0x00, 0x03, AlgECDSAP256SHA256}, pub...)
	return RR{Name: owner, Type: TypeDNSKEY, Class: ClassIN, TTL: testTTL, Rdata: rdata}
}

// ecdsaRawSign signs digest and returns the fixed-width R||S encoding.
func ecdsaRawSign(t *testing.T, priv *ecdsa.PrivateKey, digest []byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	var sigStruct struct{ R, S *big.Int }
	sigStruct.R, sigStruct.S = r, s
	der, err := asn1.Marshal(sigStruct)
	if err != nil {
		t.Fatalf("asn1: %v", err)
	}
	var parsed struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &parsed); err != nil {
		t.Fatalf("asn1 decode: %v", err)
	}
	out := make([]byte, 64)
	parsed.R.FillBytes(out[:32])
	parsed.S.FillBytes(out[32:])
	return out
}

// signRRset produces a valid RRSIG RR covering rrset.
func signRRset(
	t *testing.T,
	rrset []RR,
	signer Name,
	priv *ecdsa.PrivateKey,
	dnskeyRR RR,
	labels uint8,
	inception, expiration uint32,
) RR {
	t.Helper()
	key, err := ParseDNSKEY(dnskeyRR.Rdata)
	if err != nil {
		t.Fatalf("dnskey: %v", err)
	}
	tag := DNSKEYKeyTag(key)
	sig := &RRSIG{
		TypeCovered: rrset[0].Type,
		Algorithm:   AlgECDSAP256SHA256,
		Labels:      labels,
		OrigTTL:     rrset[0].TTL,
		Expiration:  expiration,
		Inception:   inception,
		KeyTag:      tag,
		Signer:      signer,
	}
	canonical, err := CanonicalRRSet(rrset, sig.OrigTTL)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	digest := sha256.Sum256(SignedData(sig, canonical))
	sig.Signature = ecdsaRawSign(t, priv, digest[:])

	rdata := make([]byte, 0, 18+len(signer.Wire())+len(sig.Signature))
	rdata = appendU16(rdata, sig.TypeCovered)
	rdata = append(rdata, sig.Algorithm, sig.Labels)
	rdata = appendU32(rdata, sig.OrigTTL)
	rdata = appendU32(rdata, sig.Expiration)
	rdata = appendU32(rdata, sig.Inception)
	rdata = appendU16(rdata, sig.KeyTag)
	rdata = append(rdata, signer.Wire()...)
	rdata = append(rdata, sig.Signature...)

	return RR{
		Name:  rrset[0].Name,
		Type:  TypeRRSIG,
		Class: ClassIN,
		TTL:   testTTL,
		Rdata: rdata,
	}
}

func nsec3RR(ownerHash, nextHash, salt []byte, iterations uint16, flags uint8, types []uint16, zone Name) RR {
	labels := append([]string{Base32HexEncode(ownerHash)}, labelsToStrings(zone)...)
	owner := NameFromLabels(labels...)
	rdata := make([]byte, 0)
	rdata = append(rdata, NSEC3SHA1, flags)
	rdata = appendU16(rdata, iterations)
	rdata = append(rdata, byte(len(salt)))
	rdata = append(rdata, salt...)
	rdata = append(rdata, byte(len(nextHash)))
	rdata = append(rdata, nextHash...)
	rdata = append(rdata, encodeTypeBitmap(types)...)
	return RR{Name: owner, Type: TypeNSEC3, Class: ClassIN, TTL: testTTL, Rdata: rdata}
}

func nsecRR(owner, next Name, types []uint16) RR {
	rdata := append(next.LowerWire(), encodeTypeBitmap(types)...)
	return RR{Name: owner, Type: TypeNSEC, Class: ClassIN, TTL: testTTL, Rdata: rdata}
}

func labelsToStrings(n Name) []string {
	out := make([]string, n.LabelCount())
	for i, l := range n.Labels() {
		out[i] = string(l)
	}
	return out
}

func fixedTime(sec uint32) time.Time { return time.Unix(int64(sec), 0).UTC() }

// packMessage serializes a minimal message using name compression so the
// parser path is exercised end to end.
func packMessage(m *Message) []byte {
	wire := make([]byte, 12)
	wire[0], wire[1] = byte(m.ID>>8), byte(m.ID)
	wire[2], wire[3] = byte(m.Flags>>8), byte(m.Flags)
	pointers := map[string]int{}

	putName := func(n Name) []byte {
		var out []byte
		for i, label := range n.Labels() {
			suffix := Name{labels: n.Labels()[i:]}
			key := suffix.String()
			if pos, ok := pointers[key]; ok {
				out = append(out, byte(0xC0|pos>>8), byte(pos&0xFF))
				return out
			}
			pointers[key] = 0 // filled below once position known
			pos := len(wire) + len(out)
			pointers[key] = pos
			out = append(out, byte(len(label)))
			out = append(out, label...)
		}
		out = append(out, 0)
		return out
	}

	counts := [3]int{len(m.Question), len(m.Answer), len(m.Authority)}
	wire[4], wire[5] = byte(counts[0]>>8), byte(counts[0])
	wire[6], wire[7] = byte(counts[1]>>8), byte(counts[1])
	wire[8], wire[9] = byte(counts[2]>>8), byte(counts[2])
	wire[10], wire[11] = 0, byte(len(m.Additional))

	for _, q := range m.Question {
		wire = append(wire, putName(q.Name)...)
		wire = appendU16(wire, q.Type)
		wire = appendU16(wire, q.Class)
	}
	all := [][]RR{m.Answer, m.Authority, m.Additional}
	for _, section := range all {
		for _, rr := range section {
			wire = append(wire, putName(rr.Name)...)
			wire = appendU16(wire, rr.Type)
			wire = appendU16(wire, rr.Class)
			wire = appendU32(wire, rr.TTL)
			wire = appendU16(wire, uint16(len(rr.Rdata)))
			wire = append(wire, rr.Rdata...)
		}
	}
	return wire
}
