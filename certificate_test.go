package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"
)

func testCertificate(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "named-p256"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, key
}

func TestParseCertificateAcceptsNamedP256(t *testing.T) {
	der, _ := testCertificate(t)

	cert, err := ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatalf("PublicKeyAlgorithm = %v, want ECDSA", cert.PublicKeyAlgorithm)
	}

	if err := ValidateCertificate(der); err != nil {
		t.Fatalf("ValidateCertificate(DER): %v", err)
	}
	if err := ValidateCertificate(cert); err != nil {
		t.Fatalf("ValidateCertificate(cert): %v", err)
	}
}

func TestParseCertificatePEM(t *testing.T) {
	der, _ := testCertificate(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	cert, err := ParseCertificatePEM(pemBytes)
	if err != nil {
		t.Fatalf("ParseCertificatePEM: %v", err)
	}
	if cert.Subject.CommonName != "named-p256" {
		t.Fatalf("CommonName = %q", cert.Subject.CommonName)
	}
}

func TestParseCertificateRejectsExplicitP256Parameters(t *testing.T) {
	der, _ := testCertificate(t)
	explicitDER := replaceSPKIParameters(t, der, explicitP256Parameters(t))

	// The point is still a 65-byte uncompressed P-256 point. Only the
	// parameters changed, so length-based heuristics would accept it.
	if _, err := ParseCertificate(explicitDER); !errors.Is(err, ErrExplicitECParameters) {
		t.Fatalf("ParseCertificate error = %v, want ErrExplicitECParameters", err)
	}
	if err := ValidateCertificate(explicitDER); !errors.Is(err, ErrExplicitECParameters) {
		t.Fatalf("ValidateCertificate error = %v, want ErrExplicitECParameters", err)
	}
}

func TestExplicitWeakCurveWithP256SizedPointIsRejected(t *testing.T) {
	// A deliberately different 256-bit curve. Its point has the same encoded
	// size as P-256, but it is hidden behind explicit parameters.
	weak := explicitCurveParameters(256, 3, 5, big.NewInt(17))

	der, _ := testCertificate(t)
	weakDER := replaceSPKIParameters(t, der, weak)
	if _, err := ParseCertificate(weakDER); !errors.Is(err, ErrExplicitECParameters) {
		t.Fatalf("ParseCertificate error = %v, want ErrExplicitECParameters", err)
	}

	if err := ValidateECParameters(weak); !errors.Is(err, ErrExplicitECParameters) {
		t.Fatalf("ValidateECParameters error = %v, want ErrExplicitECParameters", err)
	}
}

func TestValidateECParameters(t *testing.T) {
	named, err := asn1.Marshal(oidNamedCurveP256)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := ParseECParameters(named)
	if err != nil {
		t.Fatalf("named P-256: %v", err)
	}
	if !oid.Equal(oidNamedCurveP256) {
		t.Fatalf("OID = %s", oid)
	}

	tests := []struct {
		name   string
		params []byte
		want   error
	}{
		{"missing", nil, ErrMissingECParameters},
		{"explicit", explicitP256Parameters(t), ErrExplicitECParameters},
		{"null", []byte{0x05, 0x00}, ErrInvalidECParameters},
		{"unknown named curve", mustMarshal(t, asn1.ObjectIdentifier{1, 3, 132, 0, 10}), ErrUnsupportedECCurve},
		{"trailing data", append(append([]byte(nil), named...), 0), ErrInvalidECParameters},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateECParameters(tt.params); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidateSubjectPublicKeyInfo(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point := elliptic.Marshal(elliptic.P256(), key.X, key.Y)

	named := marshalSPKI(t, oidNamedCurveP256, point)
	if err := ValidateSubjectPublicKeyInfo(named); err != nil {
		t.Fatalf("named P-256 SPKI: %v", err)
	}

	explicit := marshalSPKIWithRawParameters(t, explicitP256Parameters(t), point)
	if err := ValidateSubjectPublicKeyInfo(explicit); !errors.Is(err, ErrExplicitECParameters) {
		t.Fatalf("explicit SPKI error = %v, want ErrExplicitECParameters", err)
	}

	wrongPoint := marshalSPKI(t, oidNamedCurveP384, point)
	if err := ValidateSubjectPublicKeyInfo(wrongPoint); !errors.Is(err, ErrInvalidECPublicKey) {
		t.Fatalf("wrong-size point error = %v, want ErrInvalidECPublicKey", err)
	}
}

func TestValidateSubjectPublicKeyInfoIgnoresNonECKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSubjectPublicKeyInfo(der); err != nil {
		t.Fatalf("RSA SPKI: %v", err)
	}
}

func TestIsNamedCurveRejectsCustomCurveWithP256Parameters(t *testing.T) {
	if IsNamedCurve(elliptic.P256()) != true {
		t.Fatal("P-256 should be named")
	}
	custom := &namedImpostor{Curve: elliptic.P256()}
	if IsNamedCurve(custom) {
		t.Fatal("custom curve implementation must not be treated as named")
	}
}

type namedImpostor struct {
	elliptic.Curve
}

func (c *namedImpostor) Params() *elliptic.CurveParams {
	p := *c.Curve.Params()
	p.Name = "P-256"
	return &p
}

func TestVerifyCertificateChain(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca)
	chains, err := VerifyCertificateChain(leaf, nil, roots, x509.VerifyOptions{})
	if err != nil {
		t.Fatalf("VerifyCertificateChain: %v", err)
	}
	if len(chains) == 0 || len(chains[0]) != 2 {
		t.Fatalf("unexpected chains: %d", len(chains))
	}
}

func marshalSPKI(t *testing.T, curveOID asn1.ObjectIdentifier, point []byte) []byte {
	t.Helper()
	params, err := asn1.Marshal(curveOID)
	if err != nil {
		t.Fatal(err)
	}
	return marshalSPKIWithRawParameters(t, params, point)
}

func marshalSPKIWithRawParameters(t *testing.T, params, point []byte) []byte {
	t.Helper()
	spki := struct {
		Algorithm struct {
			Algorithm  asn1.ObjectIdentifier
			Parameters asn1.RawValue
		}
		PublicKey asn1.BitString
	}{}
	spki.Algorithm.Algorithm = oidPublicKeyECDSA
	spki.Algorithm.Parameters = asn1.RawValue{FullBytes: params}
	spki.PublicKey = asn1.BitString{Bytes: point, BitLength: len(point) * 8}

	der, err := asn1.Marshal(spki)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func replaceSPKIParameters(t *testing.T, certDER, params []byte) []byte {
	t.Helper()

	var envelope struct {
		TBS                asn1.RawValue
		SignatureAlgorithm asn1.RawValue
		SignatureValue     asn1.BitString
	}
	rest, err := asn1.Unmarshal(certDER, &envelope)
	if err != nil || len(rest) != 0 {
		t.Fatalf("parse certificate envelope: %v", err)
	}

	fields, err := sequenceFields(envelope.TBS.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	if fields[0].Class == asn1.ClassContextSpecific && fields[0].Tag == 0 {
		index = 1
	}
	spkiIndex := index + 5

	var spki struct {
		Algorithm struct {
			Algorithm  asn1.ObjectIdentifier
			Parameters asn1.RawValue
		}
		PublicKey asn1.BitString
	}
	rest, err = asn1.Unmarshal(fields[spkiIndex].FullBytes, &spki)
	if err != nil || len(rest) != 0 {
		t.Fatalf("parse SPKI: %v", err)
	}
	spki.Algorithm.Parameters = asn1.RawValue{FullBytes: params}
	newSPKI, err := asn1.Marshal(spki)
	if err != nil {
		t.Fatal(err)
	}
	fields[spkiIndex] = asn1.RawValue{FullBytes: newSPKI}

	var tbs bytes.Buffer
	for _, field := range fields {
		tbs.Write(field.FullBytes)
	}
	newTBS := derWrap(0x30, tbs.Bytes())

	var out struct {
		TBS                asn1.RawValue
		SignatureAlgorithm asn1.RawValue
		SignatureValue     asn1.BitString
	}
	out.TBS = asn1.RawValue{FullBytes: newTBS}
	out.SignatureAlgorithm = envelope.SignatureAlgorithm
	out.SignatureValue = envelope.SignatureValue
	outDER, err := asn1.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return outDER
}

func explicitP256Parameters(t *testing.T) []byte {
	t.Helper()
	params := elliptic.P256().Params()
	a := new(big.Int).Sub(params.P, big.NewInt(3))
	base := elliptic.Marshal(elliptic.P256(), params.Gx, params.Gy)

	type fieldID struct {
		FieldType asn1.ObjectIdentifier
		FieldSize *big.Int
	}
	field := fieldID{
		FieldType: asn1.ObjectIdentifier{1, 2, 840, 10045, 1, 1},
		FieldSize: params.P,
	}
	curve := struct {
		A []byte
		B []byte
	}{
		A: a.Bytes(),
		B: params.B.Bytes(),
	}

	var body bytes.Buffer
	body.Write(mustMarshal(t, 1))
	body.Write(derWrap(0x30, mustMarshal(t, field)))
	body.Write(derWrap(0x30, mustMarshal(t, curve)))
	body.Write(mustMarshal(t, base))
	body.Write(mustMarshal(t, params.N))
	body.Write(mustMarshal(t, 1))
	return derWrap(0x30, body.Bytes())
}

func explicitCurveParameters(bitSize, a, b int64, order *big.Int) []byte {
	// ECParameters as defined by RFC 5480. The values do not need to describe
	// a usable curve: the test is that any explicit SEQUENCE is rejected before
	// its contents are considered.
	type fieldID struct {
		FieldType asn1.ObjectIdentifier
		FieldSize *big.Int
	}
	primeField := asn1.ObjectIdentifier{1, 2, 840, 10045, 1, 1}
	field := fieldID{FieldType: primeField, FieldSize: new(big.Int).Lsh(big.NewInt(1), uint(bitSize))}
	fieldDER := mustMarshal(nil, field)

	curve := struct {
		A []byte
		B []byte
	}{A: big.NewInt(a).Bytes(), B: big.NewInt(b).Bytes()}
	curveDER := mustMarshal(nil, curve)

	var body bytes.Buffer
	body.Write(mustMarshal(nil, 1))
	body.Write(derWrap(0x30, fieldDER))
	body.Write(derWrap(0x30, curveDER))
	body.Write(mustMarshal(nil, []byte{4, 1, 2}))
	body.Write(mustMarshal(nil, order))
	body.Write(mustMarshal(nil, 1))
	return derWrap(0x30, body.Bytes())
}

func mustMarshal(t *testing.T, value any) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}
	return der
}

func derWrap(tag byte, content []byte) []byte {
	var out bytes.Buffer
	out.WriteByte(tag)
	if len(content) < 128 {
		out.WriteByte(byte(len(content)))
	} else {
		var length [8]byte
		n := 0
		for v := len(content); v > 0; v >>= 8 {
			length[len(length)-1-n] = byte(v)
			n++
		}
		out.WriteByte(0x80 | byte(n))
		out.Write(length[len(length)-n:])
	}
	out.Write(content)
	return out.Bytes()
}
