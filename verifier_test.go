package netsec

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"os"
	"sort"
	"testing"
	"time"
)

var (
	testRootCert *x509.Certificate
	testRootKey  *rsa.PrivateKey
	testTSACert  *x509.Certificate
	testTSAKey   *rsa.PrivateKey
	testCodeCert *x509.Certificate
	testCodeKey  *rsa.PrivateKey

	testRootStart = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	testRootEnd   = time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	validGenTime  = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
)

func TestMain(m *testing.M) {
	testRootKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	testTSAKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	testCodeKey, _ = rsa.GenerateKey(rand.Reader, 2048)

	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "Test Root CA", Organization: []string{"Netsec Test"}},
		NotBefore:             testRootStart,
		NotAfter:              testRootEnd,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &testRootKey.PublicKey, testRootKey)
	if err != nil {
		panic(err)
	}
	testRootCert, err = x509.ParseCertificate(rootDER)
	if err != nil {
		panic(err)
	}

	tsaTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(101),
		Subject:               pkix.Name{CommonName: "Test TSA", Organization: []string{"Netsec Test"}},
		NotBefore:             time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
		BasicConstraintsValid: true,
	}
	tsaDER, err := x509.CreateCertificate(rand.Reader, tsaTemplate, testRootCert, &testTSAKey.PublicKey, testRootKey)
	if err != nil {
		panic(err)
	}
	testTSACert, err = x509.ParseCertificate(tsaDER)
	if err != nil {
		panic(err)
	}

	codeTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(102),
		Subject:               pkix.Name{CommonName: "Test Code Signer", Organization: []string{"Netsec Test"}},
		NotBefore:             time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
	codeDER, err := x509.CreateCertificate(rand.Reader, codeTemplate, testRootCert, &testCodeKey.PublicKey, testRootKey)
	if err != nil {
		panic(err)
	}
	testCodeCert, err = x509.ParseCertificate(codeDER)
	if err != nil {
		panic(err)
	}
	if err := testRootCert.CheckSignatureFrom(testRootCert); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestParseAndVerifyTimestampBinding(t *testing.T) {
	signature := []byte("code-signing signature bytes")
	token := makeTestToken(t, testTokenOptions{
		serial:    42,
		genTime:   validGenTime,
		signature: signature,
	})
	parsed, err := ParseTimestampToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if parsed.SerialNumber.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("serial = %v", parsed.SerialNumber)
	}
	if parsed.Policy.String() != testPolicy().String() {
		t.Fatalf("policy = %s", parsed.Policy)
	}
	if parsed.MessageImprint.Hash != crypto.SHA256 || !bytes.Equal(parsed.HashedMessage, digestSHA256(signature)) {
		t.Fatalf("unexpected imprint: %#v", parsed.MessageImprint)
	}
	if parsed.IssuerName == "" || parsed.TimestampSigner == nil {
		t.Fatalf("issuer identity was not extracted")
	}

	verifier := newTestVerifier(t, nil)
	binding, err := verifier.Verify(signature, token, testCodeCert)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !binding.Covers(signature) {
		t.Fatalf("binding does not cover signature")
	}
	if binding.GenTime != validGenTime {
		t.Fatalf("genTime = %s", binding.GenTime)
	}
	if binding.CacheHit {
		t.Fatalf("first verification unexpectedly reported a cache hit")
	}

	second, err := verifier.Verify(signature, token, testCodeCert)
	if err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if !second.CacheHit {
		t.Fatalf("second verification did not use the imprint+issuer cache")
	}
}

func TestChainVerificationWithoutEmbeddedRoot(t *testing.T) {
	signature := []byte("chain without embedded root")
	token := makeTestToken(t, testTokenOptions{
		serial:       48,
		genTime:      validGenTime,
		signature:    signature,
		omitRootCert: true,
	})
	parsed, err := ParseTimestampToken(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Certificates) != 1 {
		t.Fatalf("certificate count = %d", len(parsed.Certificates))
	}
	verifier := newTestVerifier(t, nil)
	if _, err := verifier.Verify(signature, token, testCodeCert); err != nil {
		t.Fatalf("verify without embedded root: %v", err)
	}
}

func TestTamperedTokenSignatureAndMissingSignedAttributesRejected(t *testing.T) {
	signature := []byte("tamper signature")
	valid := makeTestToken(t, testTokenOptions{serial: 49, genTime: validGenTime, signature: signature})
	tampered := bytes.Clone(valid)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := ParseTimestampToken(tampered); !errors.Is(err, ErrTokenSignature) {
		t.Fatalf("want token signature error, got %v", err)
	}

	unsigned := makeTestToken(t, testTokenOptions{
		serial:          54,
		genTime:         validGenTime,
		signature:       signature,
		omitSignedAttrs: true,
	})
	if _, err := ParseTimestampToken(unsigned); !errors.Is(err, ErrTokenSignature) {
		t.Fatalf("want missing signed attributes rejection, got %v", err)
	}
}

func TestOldTokenForNewSignature(t *testing.T) {
	oldSignature := []byte("old artifact signature")
	newSignature := []byte("new artifact signature")
	token := makeTestToken(t, testTokenOptions{serial: 43, genTime: validGenTime, signature: oldSignature})
	verifier := newTestVerifier(t, nil)
	_, err := verifier.Verify(newSignature, token, testCodeCert)
	if !errors.Is(err, ErrImprintMismatch) {
		t.Fatalf("want imprint mismatch, got %v", err)
	}
}

func TestDigestAlgorithmMismatch(t *testing.T) {
	signature := []byte("algorithm bound signature")
	token := makeTestToken(t, testTokenOptions{serial: 44, genTime: validGenTime, signature: signature})
	verifier := newTestVerifier(t, nil)

	_, err := verifier.VerifyRequest(VerificationRequest{
		Token:             token,
		Signature:         signature,
		HashAlgorithm:     crypto.SHA1,
		SignerCertificate: testCodeCert,
	})
	if !errors.Is(err, ErrDigestAlgorithmMismatch) {
		t.Fatalf("want digest algorithm mismatch, got %v", err)
	}

	sha1Digest := make([]byte, crypto.SHA1.Size())
	_, err = verifier.VerifyDigest(sha1Digest, crypto.SHA1, token, testCodeCert)
	if !errors.Is(err, ErrDigestAlgorithmMismatch) {
		t.Fatalf("want digest length mismatch, got %v", err)
	}
}

func TestSignerCertificateExpiredAtGenTime(t *testing.T) {
	signature := []byte("expired signer signature")
	token := makeTestToken(t, testTokenOptions{
		serial:    45,
		genTime:   time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC),
		signature: signature,
	})
	verifier := newTestVerifier(t, nil, WithClock(func() time.Time {
		return time.Date(2027, 7, 1, 0, 0, 0, 0, time.UTC)
	}))
	_, err := verifier.Verify(signature, token, testCodeCert)
	if !errors.Is(err, ErrSignerCertificateExpired) {
		t.Fatalf("want signer certificate expired, got %v", err)
	}
}

func TestRejectsNonTSTInfoAndMultipleTokens(t *testing.T) {
	signature := []byte("content type signature")
	token := makeTestToken(t, testTokenOptions{
		serial:           46,
		genTime:          validGenTime,
		signature:        signature,
		encapContentType: asn1.ObjectIdentifier{1, 2, 3, 4, 5},
		contentTypeAttr:  asn1.ObjectIdentifier{1, 2, 3, 4, 5},
	})
	if _, err := ParseTimestampToken(token); !errors.Is(err, ErrUnsupportedContentType) {
		t.Fatalf("want unsupported content type, got %v", err)
	}
	badSignedAttribute := makeTestToken(t, testTokenOptions{
		serial:          55,
		genTime:         validGenTime,
		signature:       signature,
		contentTypeAttr: asn1.ObjectIdentifier{1, 2, 3, 4, 6},
	})
	if _, err := ParseTimestampToken(badSignedAttribute); !errors.Is(err, ErrUnsupportedContentType) {
		t.Fatalf("want signed content type rejection, got %v", err)
	}

	validToken := makeTestToken(t, testTokenOptions{serial: 47, genTime: validGenTime, signature: signature})
	verifier := newTestVerifier(t, nil)
	_, err := verifier.VerifyTokens(signature, [][]byte{validToken, validToken}, testCodeCert)
	if !errors.Is(err, ErrMultipleTokens) {
		t.Fatalf("want multiple token rejection, got %v", err)
	}
}

func TestDuplicateSerialRejectedPerIssuer(t *testing.T) {
	signatureA := []byte("serial A")
	signatureB := []byte("serial B")
	tokenA := makeTestToken(t, testTokenOptions{serial: 50, genTime: validGenTime, signature: signatureA})
	tokenB := makeTestToken(t, testTokenOptions{serial: 50, genTime: validGenTime, signature: signatureB})
	verifier := newTestVerifier(t, nil)
	if _, err := verifier.Verify(signatureA, tokenA, testCodeCert); err != nil {
		t.Fatalf("first token: %v", err)
	}
	_, err := verifier.Verify(signatureB, tokenB, testCodeCert)
	if !errors.Is(err, ErrDuplicateSerial) {
		t.Fatalf("want duplicate serial, got %v", err)
	}
}

func TestIssuerAllowList(t *testing.T) {
	signature := []byte("issuer allow list")
	token := makeTestToken(t, testTokenOptions{serial: 51, genTime: validGenTime, signature: signature})
	verifier := newTestVerifier(t, nil, WithTrustedIssuers("CN=Test Root CA"))
	if _, err := verifier.Verify(signature, token, testCodeCert); err != nil {
		t.Fatalf("trusted issuer chain: %v", err)
	}

	untrusted := newTestVerifier(t, nil, WithTrustedIssuers("CN=Some Other TSA"))
	_, err := untrusted.Verify(signature, token, testCodeCert)
	if !errors.Is(err, ErrIssuerNotTrusted) {
		t.Fatalf("want issuer not trusted, got %v", err)
	}
}

func TestCacheExpiresAndRevocationInvalidates(t *testing.T) {
	signature := []byte("cache signature")
	token := makeTestToken(t, testTokenOptions{serial: 52, genTime: validGenTime, signature: signature})
	now := validGenTime.Add(10 * time.Minute)
	clock := func() time.Time { return now }
	cache := NewTokenCache(time.Minute)
	revocations := NewRevocationList()
	verifier := newTestVerifier(t, nil, WithClock(clock), WithCache(cache), WithRevocationChecker(revocations))

	if _, err := verifier.Verify(signature, token, testCodeCert); err != nil {
		t.Fatalf("first: %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache length = %d", cache.Len())
	}
	key := NewCacheKey(
		MessageImprint{HashAlgorithm: OIDSHA256, HashedMessage: digestSHA256(signature)},
		certIdentity(testTSACert),
	)
	if _, ok := cache.Get(key, now); !ok {
		t.Fatalf("expected cache entry under imprint+issuer key")
	}

	now = now.Add(2 * time.Minute)
	binding, err := verifier.Verify(signature, token, testCodeCert)
	if err != nil {
		t.Fatalf("after cache ttl: %v", err)
	}
	if binding.CacheHit {
		t.Fatalf("expired cache entry was reused")
	}
	if _, ok := cache.Get(key, now); !ok {
		t.Fatalf("revalidation did not repopulate cache")
	}

	revocations.Revoke(testTSACert, now)
	_, err = verifier.Verify(signature, token, testCodeCert)
	if !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("want revoked rejection, got %v", err)
	}
	if _, ok := cache.Get(key, now); ok {
		t.Fatalf("revoked entry remained in cache")
	}
}

func TestCoSigningAccepted(t *testing.T) {
	signature := []byte("co-signed timestamp")
	secondKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	secondTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(103),
		Subject:               pkix.Name{CommonName: "Second TSA", Organization: []string{"Netsec Test"}},
		NotBefore:             time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
		BasicConstraintsValid: true,
	}
	secondDER, err := x509.CreateCertificate(rand.Reader, secondTemplate, testRootCert, &secondKey.PublicKey, testRootKey)
	if err != nil {
		t.Fatal(err)
	}
	secondCert, err := x509.ParseCertificate(secondDER)
	if err != nil {
		t.Fatal(err)
	}
	token := makeTestToken(t, testTokenOptions{
		serial:    53,
		genTime:   validGenTime,
		signature: signature,
		coSigners: []testSigner{{cert: secondCert, key: secondKey}},
	})
	verifier := newTestVerifier(t, nil)
	binding, err := verifier.Verify(signature, token, testCodeCert)
	if err != nil {
		t.Fatalf("co-signed token: %v", err)
	}
	if len(binding.Token.Signers) != 2 {
		t.Fatalf("signer count = %d", len(binding.Token.Signers))
	}
}

func TestDualTokensBindToOwnSignatures(t *testing.T) {
	signatureA := []byte("artifact A signature")
	signatureB := []byte("artifact B signature")
	tokenA := makeTestToken(t, testTokenOptions{serial: 60, genTime: validGenTime, signature: signatureA})
	tokenB := makeTestToken(t, testTokenOptions{serial: 61, genTime: validGenTime.Add(time.Hour), signature: signatureB})
	verifier := newTestVerifier(t, nil)
	bindings, err := verifier.VerifyAll([]VerificationRequest{
		{Token: tokenA, Signature: signatureA, SignerCertificate: testCodeCert},
		{Token: tokenB, Signature: signatureB, SignerCertificate: testCodeCert},
	})
	if err != nil {
		t.Fatalf("dual verification: %v", err)
	}
	if !bindings[0].Covers(signatureA) || bindings[0].Covers(signatureB) {
		t.Fatalf("token A leaked to artifact B")
	}
	if !bindings[1].Covers(signatureB) || bindings[1].Covers(signatureA) {
		t.Fatalf("token B leaked to artifact A")
	}
	if bindings[0].GenTime != validGenTime || bindings[1].GenTime != validGenTime.Add(time.Hour) {
		t.Fatalf("timestamp times were not kept per signature")
	}
}

func newTestVerifier(t *testing.T, roots *x509.CertPool, options ...Option) *Verifier {
	t.Helper()
	if roots == nil {
		roots = x509.NewCertPool()
		roots.AddCert(testRootCert)
	}
	verifier, err := NewVerifier(VerifierConfig{Roots: roots}, options...)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

type testSigner struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
}

type testTokenOptions struct {
	serial           int64
	genTime          time.Time
	signature        []byte
	policy           asn1.ObjectIdentifier
	encapContentType asn1.ObjectIdentifier
	contentTypeAttr  asn1.ObjectIdentifier
	digestAlgorithm  asn1.ObjectIdentifier
	digestHash       crypto.Hash
	coSigners        []testSigner
	omitSignedAttrs  bool
	omitRootCert     bool
	extraCerts       []*x509.Certificate
}

func makeTestToken(t *testing.T, options testTokenOptions) []byte {
	t.Helper()
	if options.policy == nil {
		options.policy = testPolicy()
	}
	if options.encapContentType == nil {
		options.encapContentType = OIDContentTypeTSTInfo
	}
	if options.contentTypeAttr == nil {
		options.contentTypeAttr = options.encapContentType
	}
	if options.digestAlgorithm == nil {
		options.digestAlgorithm = OIDSHA256
		options.digestHash = crypto.SHA256
	}
	if options.digestHash == 0 {
		hash, _, err := hashForOID(options.digestAlgorithm)
		if err != nil {
			t.Fatal(err)
		}
		options.digestHash = hash
	}
	tstInfo := marshalTSTInfo(t, options)
	signers := append([]testSigner{{cert: testTSACert, key: testTSAKey}}, options.coSigners...)
	certificates := []*x509.Certificate{testTSACert}
	if !options.omitRootCert {
		certificates = append(certificates, testRootCert)
	}
	certificates = append(certificates, options.extraCerts...)
	for _, signer := range options.coSigners {
		certificates = append(certificates, signer.cert)
	}

	var signerInfos [][]byte
	for _, signer := range signers {
		signerInfos = append(signerInfos, marshalSignerInfo(t, signer, tstInfo, options))
	}

	var digestAlgorithms []byte
	digestAlgorithms = append(digestAlgorithms, marshalAlgorithmIdentifier(t, options.digestAlgorithm)...)
	digestAlgorithms = marshalTLVNoError(0x31, digestAlgorithms)

	var certSet []byte
	seen := make(map[string]bool)
	for _, certificate := range certificates {
		if certificate == nil || seen[string(certificate.Raw)] {
			continue
		}
		seen[string(certificate.Raw)] = true
		certSet = append(certSet, certificate.Raw...)
	}
	certSet = marshalTLVNoError(0xA0, certSet)

	encap := marshalTLVNoError(0x30, append(marshalOID(t, options.encapContentType), marshalTLVNoError(0xA0, marshalTLVNoError(0x04, tstInfo))...))
	signerSet := marshalTLVNoError(0x31, bytes.Join(signerInfos, nil))
	signedData := bytes.Join([][]byte{
		marshalInteger(t, big.NewInt(1)),
		digestAlgorithms,
		encap,
		certSet,
		signerSet,
	}, nil)
	signedData = marshalTLVNoError(0x30, signedData)
	contentInfo := marshalTLVNoError(0x30, append(marshalOID(t, OIDContentTypeSignedData), marshalTLVNoError(0xA0, signedData)...))
	return contentInfo
}

func marshalTSTInfo(t *testing.T, options testTokenOptions) []byte {
	t.Helper()
	messageImprint := marshalTLVNoError(0x30, append(
		marshalAlgorithmIdentifier(t, options.digestAlgorithm),
		marshalTLVNoError(0x04, digestWith(t, options.digestHash, options.signature))...,
	))
	fields := [][]byte{
		marshalInteger(t, big.NewInt(1)),
		marshalOID(t, options.policy),
		messageImprint,
		marshalInteger(t, big.NewInt(options.serial)),
		marshalGeneralizedTime(t, options.genTime),
	}
	return marshalTLVNoError(0x30, bytes.Join(fields, nil))
}

func marshalSignerInfo(t *testing.T, signer testSigner, tstInfo []byte, options testTokenOptions) []byte {
	t.Helper()
	issuerAndSerialContent := append(
		bytes.Clone(signer.cert.RawIssuer),
		marshalInteger(t, signer.cert.SerialNumber)...,
	)
	issuerAndSerial := marshalTLVNoError(0x30, issuerAndSerialContent)
	digestAlgorithm := marshalAlgorithmIdentifier(t, options.digestAlgorithm)
	signatureAlgorithm := marshalAlgorithmIdentifier(t, OIDRSAEncryption)
	attrsContent := signedAttributeContent(t, options)
	signedAttributesDER := marshalTLVNoError(0x31, attrsContent)
	digest := digestWith(t, options.digestHash, signedAttributesDER)
	signature, err := rsa.SignPKCS1v15(rand.Reader, signer.key, options.digestHash, digest)
	if err != nil {
		t.Fatal(err)
	}
	var signedAttributesField []byte
	if options.omitSignedAttrs {
		signature, err = rsa.SignPKCS1v15(rand.Reader, signer.key, options.digestHash, digestWith(t, options.digestHash, tstInfo))
		if err != nil {
			t.Fatal(err)
		}
	} else {
		signedAttributesField = marshalTLVNoError(0xA0, attrsContent)
	}
	return marshalTLVNoError(0x30, bytes.Join([][]byte{
		marshalInteger(t, big.NewInt(1)),
		issuerAndSerial,
		digestAlgorithm,
		signedAttributesField,
		signatureAlgorithm,
		marshalTLVNoError(0x04, signature),
	}, nil))
}

func signedAttributeContent(t *testing.T, options testTokenOptions) []byte {
	t.Helper()
	contentTypeAttribute := marshalAttribute(t, OIDContentType, marshalOID(t, options.contentTypeAttr))
	messageDigestAttribute := marshalAttribute(t, OIDMessageDigest, marshalTLVNoError(0x04, digestWith(t, options.digestHash, marshalTSTInfo(t, options))))
	attributes := [][]byte{contentTypeAttribute, messageDigestAttribute}
	sort.Slice(attributes, func(i, j int) bool {
		return bytes.Compare(attributes[i], attributes[j]) < 0
	})
	return bytes.Join(attributes, nil)
}

func marshalAttribute(t *testing.T, oid asn1.ObjectIdentifier, value []byte) []byte {
	t.Helper()
	return marshalTLVNoError(0x30, append(marshalOID(t, oid), marshalTLVNoError(0x31, value)...))
}

func marshalAlgorithmIdentifier(t *testing.T, oid asn1.ObjectIdentifier) []byte {
	t.Helper()
	return marshalTLVNoError(0x30, append(marshalOID(t, oid), marshalTLVNoError(0x05, nil)...))
}

func marshalOID(t *testing.T, oid asn1.ObjectIdentifier) []byte {
	t.Helper()
	encoded, err := asn1.Marshal(oid)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func marshalInteger(t *testing.T, value *big.Int) []byte {
	t.Helper()
	encoded, err := asn1.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func marshalGeneralizedTime(t *testing.T, value time.Time) []byte {
	t.Helper()
	encoded, err := asn1.MarshalWithParams(value, "generalized")
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func marshalTLVNoError(tag byte, content []byte) []byte {
	encoded, err := marshalTLV(tag, content)
	if err != nil {
		panic(err)
	}
	return encoded
}

func digestSHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func digestWith(t *testing.T, hash crypto.Hash, data []byte) []byte {
	t.Helper()
	digest, err := digestBytes(hash, data)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func testPolicy() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1}
}
