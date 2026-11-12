package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"testing"
	"time"
)

const testZone = "example.test."

type testKey struct {
	rec  DNSKEYRecord
	priv crypto.PrivateKey
}

func genECDSA(t *testing.T, alg uint8) testKey {
	t.Helper()
	curve := elliptic.P256()
	coordLen := 32
	if alg == AlgECDSAP384 {
		curve = elliptic.P384()
		coordLen = 48
	}
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	raw := make([]byte, 2*coordLen)
	priv.X.FillBytes(raw[:coordLen])
	priv.Y.FillBytes(raw[coordLen:])
	return testKey{
		rec:  DNSKEYRecord{Flags: 257, Protocol: 3, Algorithm: alg, PublicKey: raw},
		priv: priv,
	}
}

func genEd25519(t *testing.T) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	return testKey{
		rec:  DNSKEYRecord{Flags: 257, Protocol: 3, Algorithm: AlgEd25519, PublicKey: []byte(pub)},
		priv: priv,
	}
}

func genRSA(t *testing.T) testKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	exp := big.NewInt(int64(priv.E)).Bytes()
	var raw []byte
	if len(exp) > 255 {
		raw = append(raw, 0, byte(len(exp)>>8), byte(len(exp)))
	} else {
		raw = append(raw, byte(len(exp)))
	}
	raw = append(raw, exp...)
	raw = append(raw, priv.N.Bytes()...)
	return testKey{
		rec:  DNSKEYRecord{Flags: 257, Protocol: 3, Algorithm: AlgRSASHA256, PublicKey: raw},
		priv: priv,
	}
}

func asZSK(k testKey) testKey {
	k.rec.Flags = 256
	return k
}

func signerOf(t *testing.T, k testKey) Signer {
	t.Helper()
	pub, err := ParsePublicKey(k.rec)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	return Signer{Key: k.rec, PublicKey: pub}
}

func dsFor(t *testing.T, zone string, k DNSKEYRecord, digestType uint8) DSRecord {
	t.Helper()
	digest, err := DSDigest(zone, k, digestType)
	if err != nil {
		t.Fatalf("compute DS digest: %v", err)
	}
	return DSRecord{KeyTag: KeyTag(k), Algorithm: k.Algorithm, DigestType: digestType, Digest: digest}
}

// signRRset 用指定私钥在 at 时刻对 RRset 签名，RRSIG 有效期窗口为 at±1h。
func signRRset(t *testing.T, zone string, typ uint16, rrs []DNSRecord, k testKey, at time.Time) *RRSIGRecord {
	t.Helper()
	sig := RRSIGRecord{
		TypeCovered: typ,
		Algorithm:   k.rec.Algorithm,
		Labels:      uint8(labelCount(zone)),
		OrigTTL:     3600,
		Inception:   uint32(at.Unix()) - 3600,
		Expiration:  uint32(at.Unix()) + 3600,
		KeyTag:      KeyTag(k.rec),
		SignerName:  zone,
	}
	input := signedData(RRset{Name: zone, Type: typ, Records: rrs}, sig)

	switch priv := k.priv.(type) {
	case *ecdsa.PrivateKey:
		h, err := hashForSig(k.rec.Algorithm)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		hasher := h.New()
		hasher.Write(input)
		sig.Signature, err = ecdsa.SignASN1(rand.Reader, priv, hasher.Sum(nil))
		if err != nil {
			t.Fatalf("ecdsa sign: %v", err)
		}
	case ed25519.PrivateKey:
		sig.Signature = ed25519.Sign(priv, input)
	case *rsa.PrivateKey:
		h, err := hashForSig(k.rec.Algorithm)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		hasher := h.New()
		hasher.Write(input)
		sig.Signature, err = rsa.SignPKCS1v15(rand.Reader, priv, h, hasher.Sum(nil))
		if err != nil {
			t.Fatalf("rsa sign: %v", err)
		}
	default:
		t.Fatalf("unsupported test key type %T", k.priv)
	}
	return &sig
}

type fixture struct {
	zone string
	ksk  testKey
	ksk2 testKey
	zsk  testKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{
		zone: testZone,
		ksk:  genECDSA(t, AlgECDSAP256),
		ksk2: genECDSA(t, AlgECDSAP256),
		zsk:  asZSK(genECDSA(t, AlgECDSAP256)),
	}
}

func (f *fixture) signers(t *testing.T) []Signer {
	return []Signer{signerOf(t, f.ksk), signerOf(t, f.ksk2), signerOf(t, f.zsk)}
}

func (f *fixture) currentDS(t *testing.T) []DSRecord {
	return []DSRecord{dsFor(t, f.zone, f.ksk.rec, DigestSHA256)}
}

// observation 构造一台服务器的观察结果，签名时刻为 at。
func (f *fixture) observation(t *testing.T, at time.Time, server string,
	cds []DSRecord, cdns []DNSKEYRecord, signer testKey) ServerObservation {
	t.Helper()
	o := ServerObservation{Server: server, CDS: cds, CDNSKEY: cdns}
	if len(cds) > 0 {
		o.CDSSig = signRRset(t, f.zone, TypeCDS, dsAsRecords(cds), signer, at)
	}
	if len(cdns) > 0 {
		o.CDNSKEYSig = signRRset(t, f.zone, TypeCDNSKEY, dnskeyAsRecords(cdns), signer, at)
	}
	return o
}

func (f *fixture) poll(at time.Time, servers ...ServerObservation) Poll {
	return Poll{Zone: f.zone, At: at, Servers: servers}
}

func testConfig(clock func() time.Time) Config {
	return Config{
		RequireHours:      24,
		PollInterval:      time.Hour,
		MaxClockSkew:      time.Minute,
		DefaultDigestType: DigestSHA256,
		Now:               clock,
	}
}

// mutableClock 返回一个可推进的测试时钟及其读取闭包。
func mutableClock(start time.Time) (*time.Time, func() time.Time) {
	c := start
	return &c, func() time.Time { return c }
}
