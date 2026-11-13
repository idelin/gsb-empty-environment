package netsec

import (
	"crypto/dsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"
)

var (
	testParamsA = &dsa.Parameters{P: big.NewInt(503), Q: big.NewInt(251), G: big.NewInt(4)}
	testParamsB = &dsa.Parameters{P: big.NewInt(479), Q: big.NewInt(239), G: big.NewInt(4)}
)

type testValidity struct {
	NotBefore, NotAfter time.Time
}

type testTBS struct {
	Version            int `asn1:"optional,explicit,default:0,tag:0"`
	SerialNumber       *big.Int
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Issuer             pkix.RDNSequence
	Validity           testValidity
	Subject            pkix.RDNSequence
	PublicKey          testPublicKeyInfo
}

type testPublicKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

func dsaParamsDER(t *testing.T, params *dsa.Parameters) []byte {
	t.Helper()
	der, err := asn1.Marshal(dsaParameterSet{P: params.P, Q: params.Q, G: params.G})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func makeDSAKey(t *testing.T, params *dsa.Parameters) *dsa.PrivateKey {
	t.Helper()
	key := &dsa.PrivateKey{PublicKey: dsa.PublicKey{Parameters: *params}}
	if err := dsa.GenerateKey(key, rand.Reader); err != nil {
		t.Fatal(err)
	}
	return key
}

func makeDSACert(
	t *testing.T,
	subject, issuer string,
	key *dsa.PrivateKey,
	keyParams *dsa.Parameters,
	issuerKey *dsa.PrivateKey,
	signatureParams *dsa.Parameters,
) []byte {
	t.Helper()

	sigAlg := pkix.AlgorithmIdentifier{Algorithm: oidDSAWithSHA256}
	if signatureParams != nil {
		sigAlg.Parameters = asn1.RawValue{FullBytes: dsaParamsDER(t, signatureParams)}
	}

	keyAlg := pkix.AlgorithmIdentifier{Algorithm: oidPublicKeyDSA}
	if keyParams != nil {
		keyAlg.Parameters = asn1.RawValue{FullBytes: dsaParamsDER(t, keyParams)}
	}
	yDER, err := asn1.Marshal(key.Y)
	if err != nil {
		t.Fatal(err)
	}

	tbs := testTBS{
		Version:            2,
		SerialNumber:       big.NewInt(1),
		SignatureAlgorithm: sigAlg,
		Issuer:             pkix.Name{CommonName: issuer}.ToRDNSequence(),
		Validity: testValidity{
			NotBefore: time.Now().Add(-time.Hour),
			NotAfter:  time.Now().Add(time.Hour),
		},
		Subject: pkix.Name{CommonName: subject}.ToRDNSequence(),
		PublicKey: testPublicKeyInfo{
			Algorithm: keyAlg,
			PublicKey: asn1.BitString{Bytes: yDER, BitLength: 8 * len(yDER)},
		},
	}
	tbsDER, err := asn1.Marshal(tbs)
	if err != nil {
		t.Fatal(err)
	}

	if issuerKey == nil {
		issuerKey = key
	}
	digest := sha256.Sum256(tbsDER)
	r, s, err := dsa.Sign(rand.Reader, issuerKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sigDER, err := asn1.Marshal(dsaSignature{R: r, S: s})
	if err != nil {
		t.Fatal(err)
	}
	sigAlgDER, err := asn1.Marshal(sigAlg)
	if err != nil {
		t.Fatal(err)
	}

	envelope := certificateEnvelope{
		TBS:                asn1.RawValue{FullBytes: tbsDER},
		SignatureAlgorithm: asn1.RawValue{FullBytes: sigAlgDER},
		SignatureValue:     asn1.BitString{Bytes: sigDER, BitLength: 8 * len(sigDER)},
	}
	der, err := asn1.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func parseTestCertificate(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("x509.ParseCertificate: %v", err)
	}
	return cert
}

func TestDSAInheritedSignatureParameters(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	childKey := makeDSAKey(t, testParamsA)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	childDER := makeDSACert(t, "child", "root", childKey, testParamsA, rootKey, nil)

	root := parseTestCertificate(t, rootDER)
	child := parseTestCertificate(t, childDER)

	if err := VerifyChain([]*x509.Certificate{child, root}); err != nil {
		t.Fatalf("inherited parameters should verify: %v", err)
	}
	if err := VerifyChain(root, child); err != nil {
		t.Fatalf("root-first chain should verify: %v", err)
	}
	if err := NewChain(child, root).Verify(); err != nil {
		t.Fatalf("Chain.Verify should verify: %v", err)
	}
}

func TestDSAExplicitEqualSignatureParameters(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	childKey := makeDSAKey(t, testParamsA)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	childDER := makeDSACert(t, "child", "root", childKey, testParamsA, rootKey, testParamsA)

	root := parseTestCertificate(t, rootDER)
	child := parseTestCertificate(t, childDER)

	if err := VerifyChain([]*x509.Certificate{child, root}); err != nil {
		t.Fatalf("equal parameters should verify: %v", err)
	}
}

func TestDSASwappedSignatureParametersFail(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	childKey := makeDSAKey(t, testParamsA)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	childDER := makeDSACert(t, "child", "root", childKey, testParamsA, rootKey, testParamsB)

	root := parseTestCertificate(t, rootDER)
	child := parseTestCertificate(t, childDER)

	err := VerifyChain([]*x509.Certificate{child, root})
	if err == nil {
		t.Fatal("signature parameters were replaced but verification succeeded")
	}
	if !errors.Is(err, ErrDSAParameterMismatch) {
		t.Fatalf("expected parameter mismatch, got %v", err)
	}
}

func TestDSASwappedPublicKeyParametersFail(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	childKey := makeDSAKey(t, testParamsB)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	childDER := makeDSACert(t, "child", "root", childKey, testParamsB, rootKey, nil)

	root := parseTestCertificate(t, rootDER)
	child := parseTestCertificate(t, childDER)

	err := VerifyChain([]*x509.Certificate{child, root})
	if err == nil {
		t.Fatal("child public key parameters were replaced but verification succeeded")
	}
	if !errors.Is(err, ErrDSAParameterMismatch) {
		t.Fatalf("expected parameter mismatch, got %v", err)
	}
}

func TestDSAInheritedPublicKeyParametersAcrossChain(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	intermediateKey := makeDSAKey(t, testParamsA)
	leafKey := makeDSAKey(t, testParamsA)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	intermediateDER := makeDSACert(t, "intermediate", "root", intermediateKey, nil, rootKey, nil)
	leafDER := makeDSACert(t, "leaf", "intermediate", leafKey, nil, intermediateKey, nil)

	if err := VerifyDERChain([][]byte{leafDER, intermediateDER, rootDER}); err != nil {
		t.Fatalf("inherited public key parameters should verify: %v", err)
	}
	if err := VerifyDERChain([][]byte{rootDER, intermediateDER, leafDER}); err != nil {
		t.Fatalf("root-first DER chain should verify: %v", err)
	}
}

func TestDSAIntermediateParameterSwapFails(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	intermediateKey := makeDSAKey(t, testParamsB)
	leafKey := makeDSAKey(t, testParamsB)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	intermediateDER := makeDSACert(t, "intermediate", "root", intermediateKey, testParamsB, rootKey, nil)
	leafDER := makeDSACert(t, "leaf", "intermediate", leafKey, nil, intermediateKey, nil)

	err := VerifyDERChain([][]byte{leafDER, intermediateDER, rootDER})
	if err == nil {
		t.Fatal("mid-chain parameter replacement succeeded")
	}
	if !errors.Is(err, ErrDSAParameterMismatch) {
		t.Fatalf("expected parameter mismatch, got %v", err)
	}
}

func TestDSAInheritedParametersWithPEM(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	childKey := makeDSAKey(t, testParamsA)

	rootDER := makeDSACert(t, "root", "root", rootKey, testParamsA, nil, nil)
	childDER := makeDSACert(t, "child", "root", childKey, nil, rootKey, nil)

	var data []byte
	for _, der := range [][]byte{childDER, rootDER} {
		data = append(data, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err := VerifyPEMChain(data); err != nil {
		t.Fatalf("inherited PEM chain should verify: %v", err)
	}
}

func TestDSARootWithoutParametersFails(t *testing.T) {
	rootKey := makeDSAKey(t, testParamsA)
	rootDER := makeDSACert(t, "root", "root", rootKey, nil, nil, nil)

	err := VerifyDERChain([][]byte{rootDER})
	if !errors.Is(err, ErrDSAParametersMissing) {
		t.Fatalf("expected missing parameters, got %v", err)
	}
}

func TestParametersEqual(t *testing.T) {
	if !ParametersEqual(testParamsA, &dsa.Parameters{
		P: new(big.Int).Set(testParamsA.P),
		Q: new(big.Int).Set(testParamsA.Q),
		G: new(big.Int).Set(testParamsA.G),
	}) {
		t.Fatal("copied parameters should compare equal")
	}
	if ParametersEqual(testParamsA, testParamsB) {
		t.Fatal("different parameters compared equal")
	}
	if ParametersEqual(nil, testParamsA) {
		t.Fatal("nil parameters compared equal to non-nil parameters")
	}
}
