package rfc3161

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

func mustTSA(t *testing.T, eku ekuSpec) *testTSA {
	t.Helper()
	tsa, err := newTestTSA(eku)
	if err != nil {
		t.Fatalf("newTestTSA: %v", err)
	}
	return tsa
}

func stampingEKU() ekuSpec {
	return ekuSpec{critical: true, oids: []asn1.ObjectIdentifier{oidKPTimeStamping}, present: true}
}

func digestOf(data []byte) []byte {
	h := crypto.SHA256.New()
	h.Write(data)
	return h.Sum(nil)
}

func TestVerifySuccess(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	data := []byte("artifact that was signed")
	digest := digestOf(data)
	nonce := big.NewInt(987654321)
	genTime := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)

	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nonce, genTime))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	info, err := Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		Nonce:           nonce,
		PolicyWhitelist: []string{testPolicy.String()},
		Roots:           tsa.roots,
		CurrentTime:     time.Now(),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !info.GenTime.Equal(genTime) {
		t.Errorf("GenTime = %v, want %v", info.GenTime, genTime)
	}
	if info.Accuracy != 1500*time.Millisecond {
		t.Errorf("Accuracy = %v, want 1.5s", info.Accuracy)
	}
	if !info.GenTimeUpper.Equal(genTime.Add(1500 * time.Millisecond)) {
		t.Errorf("GenTimeUpper = %v", info.GenTimeUpper)
	}
	if info.Policy != testPolicy.String() {
		t.Errorf("Policy = %q", info.Policy)
	}
	if info.SerialNumber.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("SerialNumber = %v", info.SerialNumber)
	}
	if info.Signer == nil || !bytes.Equal(info.Signer.Raw, tsa.cert.Raw) {
		t.Errorf("Signer certificate mismatch")
	}
}

func TestVerifySuccessWithoutRootsAndAccuracy(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("x"))
	spec := defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
	spec.omitAcc = true
	spec.omitNonce = true
	tokenDER, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	info, err := Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if info.Accuracy != 0 {
		t.Errorf("Accuracy = %v, want 0", info.Accuracy)
	}
}

func TestMessageImprintDigestMismatch(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digestOf([]byte("a")), nil, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digestOf([]byte("b")),
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrMessageImprint) {
		t.Fatalf("err = %v, want ErrMessageImprint", err)
	}
}

func TestMessageImprintAlgorithmMismatch(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	data := []byte("same data, different algorithm")

	spec := defaultSpec(tsa, crypto.SHA256, digestOf(data), nil, time.Now().Add(-time.Minute))
	// Claim SHA-384 in the imprint while the digest value stays SHA-256-sized.
	spec.hash = crypto.SHA384
	tokenDER, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digestOf(data),
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrMessageImprint) {
		t.Fatalf("err = %v, want ErrMessageImprint", err)
	}
}

func TestNonceMismatch(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("nonce"))
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, big.NewInt(111), time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		Nonce:           big.NewInt(222),
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrNonce) {
		t.Fatalf("err = %v, want ErrNonce", err)
	}
}

func TestNonceOneSided(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("nonce2"))

	tokenWithNonce, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, big.NewInt(7), time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}
	if _, err := Verify(tokenWithNonce, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	}); !errors.Is(err, ErrNonce) {
		t.Fatalf("unexpected request-nil nonce err: %v", err)
	}

	spec := defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute))
	spec.omitNonce = true
	tokenWithoutNonce, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}
	if _, err := Verify(tokenWithoutNonce, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		Nonce:           big.NewInt(7),
		PolicyWhitelist: []string{testPolicy.String()},
	}); !errors.Is(err, ErrNonce) {
		t.Fatalf("unexpected token-nil nonce err: %v", err)
	}
}

func TestPolicyNotWhitelisted(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("policy"))
	spec := defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute))
	spec.policy = otherPolicy
	tokenDER, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrPolicy) {
		t.Fatalf("err = %v, want ErrPolicy", err)
	}
}

func TestEKUChecks(t *testing.T) {
	digest := digestOf([]byte("eku"))
	cases := []struct {
		name string
		eku  ekuSpec
	}{
		{"missing", ekuSpec{present: false}},
		{"non critical", ekuSpec{present: true, critical: false, oids: []asn1.ObjectIdentifier{oidKPTimeStamping}}},
		{"extra purpose", ekuSpec{present: true, critical: true, oids: []asn1.ObjectIdentifier{oidKPTimeStamping, {1, 3, 6, 1, 5, 5, 7, 3, 1}}}},
		{"wrong sole purpose", ekuSpec{present: true, critical: true, oids: []asn1.ObjectIdentifier{{1, 3, 6, 1, 5, 5, 7, 3, 1}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tsa := mustTSA(t, tc.eku)
			tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute)))
			if err != nil {
				t.Fatalf("buildToken: %v", err)
			}
			_, err = Verify(tokenDER, Options{
				HashAlgorithm:   crypto.SHA256,
				HashedMessage:   digest,
				PolicyWhitelist: []string{testPolicy.String()},
			})
			if !errors.Is(err, ErrEKU) {
				t.Fatalf("err = %v, want ErrEKU", err)
			}
		})
	}
}

func TestGenTimeOutsideCertificateValidity(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("expired"))

	before := defaultSpec(tsa, crypto.SHA256, digest, nil, tsa.cert.NotBefore.Add(-time.Hour))
	tokenBefore, err := buildToken(before)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}
	if _, err := Verify(tokenBefore, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
		CurrentTime:     time.Now(),
	}); !errors.Is(err, ErrTime) {
		t.Fatalf("before-validity err = %v, want ErrTime", err)
	}

	after := defaultSpec(tsa, crypto.SHA256, digest, nil, tsa.cert.NotAfter.Add(time.Hour))
	tokenAfter, err := buildToken(after)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}
	if _, err := Verify(tokenAfter, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
		CurrentTime:     time.Now(),
	}); !errors.Is(err, ErrTime) {
		t.Fatalf("after-validity err = %v, want ErrTime", err)
	}
}

func TestGenTimePlusAccuracyInFuture(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("future"))
	now := time.Now()

	// genTime is in the past but its accuracy window reaches beyond now:
	// genTime + accuracy must not be later than the verification instant.
	spec := defaultSpec(tsa, crypto.SHA256, digest, nil, now.Add(-30*time.Second))
	spec.accuracy = accuracy{Seconds: 60}
	tokenDER, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
		CurrentTime:     now,
	})
	if !errors.Is(err, ErrTime) {
		t.Fatalf("err = %v, want ErrTime", err)
	}

	// The same genTime with an accuracy window ending before now is fine.
	spec.accuracy = accuracy{Seconds: 1}
	tokenOK, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}
	if _, err := Verify(tokenOK, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
		CurrentTime:     now,
	}); err != nil {
		t.Fatalf("Verify within accuracy: %v", err)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("tamper"))
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	// Flip a byte inside the DER (the last bytes are the signature value).
	tokenDER[len(tokenDER)-1] ^= 0xFF
	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrSignature) && !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrSignature or ErrInvalidToken", err)
	}
}

func TestTamperedMessageDigestAttributeRejected(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("tamper2"))
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	// A token claiming a different data digest is a cross-file replay: the CMS
	// signature still verifies, but the messageImprint check must reject it.
	foreign := digestOf([]byte("different artifact"))
	rebuilt, err := rebuildTokenWithImprint(tsa, tokenDER, foreign, crypto.SHA256)
	if err != nil {
		t.Fatalf("rebuildTokenWithImprint: %v", err)
	}
	_, err = Verify(rebuilt, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrMessageImprint) {
		t.Fatalf("err = %v, want ErrMessageImprint", err)
	}
}

func TestUntrustedChainRejected(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("chain"))
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	otherTSA := mustTSA(t, stampingEKU())
	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
		Roots:           otherTSA.roots,
	})
	if !errors.Is(err, ErrChain) {
		t.Fatalf("err = %v, want ErrChain", err)
	}
}

func TestMalformedTokenRejected(t *testing.T) {
	digest := digestOf([]byte("malformed"))
	opts := Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	}
	for _, data := range [][]byte{nil, {}, []byte("not asn.1"), {0x30, 0x03, 0x02, 0x01, 0x01}} {
		if _, err := Verify(data, opts); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Verify(%v): err = %v, want ErrInvalidToken", data, err)
		}
	}
}

func TestOptionValidation(t *testing.T) {
	digest := digestOf([]byte("opts"))
	base := Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	}

	short := base
	short.HashedMessage = digest[:16]
	if _, err := Verify(nil, short); err == nil {
		t.Fatal("expected error for short digest")
	}

	emptyPolicy := base
	emptyPolicy.PolicyWhitelist = nil
	if _, err := Verify(nil, emptyPolicy); err == nil {
		t.Fatal("expected error for empty policy whitelist")
	}

	badPolicy := base
	badPolicy.PolicyWhitelist = []string{"not-an-oid"}
	if _, err := Verify(nil, badPolicy); err == nil {
		t.Fatal("expected error for malformed policy OID")
	}

	badHash := base
	badHash.HashAlgorithm = crypto.MD5
	if _, err := Verify(nil, badHash); err == nil {
		t.Fatal("expected error for unsupported hash")
	}
}

func TestECDSATokenRoundTrip(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ekuExt, err := buildEKUExtension(true, []asn1.ObjectIdentifier{oidKPTimeStamping})
	if err != nil {
		t.Fatalf("buildEKUExtension: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(3),
		Subject:               pkix.Name{CommonName: "Test TSA ECDSA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtraExtensions:       []pkix.Extension{ekuExt},
	}
	ecCertDER, err := x509.CreateCertificate(rand.Reader, tmpl, tsa.rootCert, &ecKey.PublicKey, tsa.rootKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	ecCert, err := x509.ParseCertificate(ecCertDER)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	digest := digestOf([]byte("ecdsa token"))
	spec := defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute))
	spec.key = ecKey
	spec.cert = ecCert
	spec.sigOID = oidECDSAWithSHA256
	tokenDER, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	if _, err := Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
		Roots:           tsa.roots,
	}); err != nil {
		t.Fatalf("Verify ECDSA token: %v", err)
	}
}

func TestMissingCertificateBagRejected(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("no cert bag"))
	spec := defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute))
	spec.includeCert = false
	tokenDER, err := buildToken(spec)
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestDigestLengthMismatchRejected(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("length"))
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	short := make([]byte, crypto.SHA256.Size()-1)
	copy(short, digest)
	_, err = Verify(tokenDER, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   short,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if err == nil {
		t.Fatal("expected error for wrong-length digest")
	}
}

func TestMultipleSignersRejected(t *testing.T) {
	tsa := mustTSA(t, stampingEKU())
	digest := digestOf([]byte("two signers"))
	tokenDER, err := buildToken(defaultSpec(tsa, crypto.SHA256, digest, nil, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("buildToken: %v", err)
	}

	// Duplicate the SignerInfo element inside the SET so two signers appear.
	var ci contentInfo
	if _, err := asn1.Unmarshal(tokenDER, &ci); err != nil {
		t.Fatal(err)
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		t.Fatal(err)
	}
	var oneSI asn1.RawValue
	if _, err := asn1.Unmarshal(sd.SignerInfos.Bytes, &oneSI); err != nil {
		t.Fatal(err)
	}
	dup := makeSetTLV(append(append([]byte{}, oneSI.FullBytes...), oneSI.FullBytes...))
	if _, err := asn1.Unmarshal(dup, &sd.SignerInfos); err != nil {
		t.Fatal(err)
	}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		t.Fatal(err)
	}
	twoToken, err := asn1.Marshal(contentInfo{
		ContentType: oidSignedData,
		Content: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      sdDER,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = Verify(twoToken, Options{
		HashAlgorithm:   crypto.SHA256,
		HashedMessage:   digest,
		PolicyWhitelist: []string{testPolicy.String()},
	})
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}
