package netsec_test

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0383"
)

type connectionExporter string

func (e connectionExporter) ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error) {
	mac := hmac.New(sha256.New, []byte(e))
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write(context)
	seed := mac.Sum(nil)

	out := make([]byte, 0, length)
	for counter := byte(0); len(out) < length; counter++ {
		blockMAC := hmac.New(sha256.New, seed)
		_, _ = blockMAC.Write([]byte{0, counter})
		out = append(out, blockMAC.Sum(nil)...)
	}
	return out[:length], nil
}

type testIdentity struct {
	root    *x509.Certificate
	leaf    *x509.Certificate
	leafKey crypto.Signer
}

func newTestIdentity(t *testing.T) testIdentity {
	t.Helper()
	leafPublic, leafPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return newTestIdentityWithLeafKey(t, leafPublic, leafPrivate)
}

func newTestIdentityWithLeafKey(t *testing.T, leafPublic crypto.PublicKey, leafPrivate crypto.Signer) testIdentity {
	t.Helper()

	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "authenticator leaf"},
		DNSNames:     []string{"service.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, leafPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return testIdentity{root: root, leaf: leaf, leafKey: leafPrivate}
}

func TestSupportedSignatureSchemes(t *testing.T) {
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		publicKey  crypto.PublicKey
		privateKey crypto.Signer
		scheme     netsec.SignatureScheme
	}{
		{
			name:       "ecdsa-p256-sha256",
			publicKey:  ecdsaKey.Public(),
			privateKey: ecdsaKey,
			scheme:     netsec.SignatureECDSASecp256r1SHA256,
		},
		{
			name:       "rsa-pss-rsae-sha256",
			publicKey:  rsaKey.Public(),
			privateKey: rsaKey,
			scheme:     netsec.SignatureRSAPSSRSAESHA256,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := newTestIdentityWithLeafKey(t, test.publicKey, test.privateKey)
			verifier := newVerifier(t, identity)
			request, err := verifier.IssueRequest()
			if err != nil {
				t.Fatal(err)
			}
			exporter := connectionExporter("connection-a")
			authenticator, err := netsec.BuildAuthenticator(request, exporter, netsec.SignOptions{
				Certificate: identity.leaf,
				PrivateKey:  identity.leafKey,
				Scheme:      test.scheme,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.Verify(authenticator, request, exporter); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func newVerifier(t *testing.T, identity testIdentity, options ...netsec.VerifierOption) *netsec.Verifier {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(identity.root)
	verifier, err := netsec.NewVerifier(roots, options...)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func buildAuthenticator(t *testing.T, request netsec.Request, exporter netsec.Exporter, identity testIdentity) []byte {
	t.Helper()
	authenticator, err := netsec.BuildAuthenticator(request, exporter, netsec.SignOptions{
		Certificate: identity.leaf,
		PrivateKey:  identity.leafKey,
		Scheme:      netsec.SignatureEd25519,
	})
	if err != nil {
		t.Fatal(err)
	}
	return authenticator
}

func TestVerifySuccessForBothRoles(t *testing.T) {
	for _, role := range []netsec.Role{netsec.RoleServer, netsec.RoleClient} {
		t.Run(roleName(role), func(t *testing.T) {
			identity := newTestIdentity(t)
			verifier := newVerifier(t, identity, netsec.WithRole(role))
			request, err := verifier.IssueRequest()
			if err != nil {
				t.Fatal(err)
			}
			wantMessageType := byte(17) // ClientCertificateRequest
			if role == netsec.RoleClient {
				wantMessageType = 13 // CertificateRequest
			}
			if got := request.Message()[0]; got != wantMessageType {
				t.Fatalf("request message type = %d, want %d", got, wantMessageType)
			}
			exporter := connectionExporter("connection-a")
			authenticator := buildAuthenticator(t, request, exporter, identity)

			verified, err := verifier.Verify(authenticator, request, exporter)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if !bytes.Equal(verified.RequestContext, request.Context()) {
				t.Fatal("verified context does not match request context")
			}
			if verified.Certificate.Subject.CommonName != identity.leaf.Subject.CommonName {
				t.Fatalf("verified certificate = %q, want %q", verified.Certificate.Subject.CommonName, identity.leaf.Subject.CommonName)
			}
		})
	}
}

func TestCertificateVerifyInputMatchesRFC9261(t *testing.T) {
	handshakeContext := []byte("connection-specific handshake context")
	request := []byte("encoded authenticator request")
	certificate := []byte("encoded Certificate message")
	transcriptHash := sha256.Sum256(append(append(append([]byte(nil), handshakeContext...), request...), certificate...))

	got, err := netsec.CertificateVerifyInput(handshakeContext, request, certificate, crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	expected := bytes.Repeat([]byte{0x20}, 64)
	expected = append(expected, []byte("Exported Authenticator")...)
	expected = append(expected, 0)
	expected = append(expected, transcriptHash[:]...)
	if !bytes.Equal(got, expected) {
		t.Fatalf("CertificateVerifyInput() mismatch\n got: %x\nwant: %x", got, expected)
	}
}

func TestTransplantAcrossConnectionsRejected(t *testing.T) {
	identity := newTestIdentity(t)
	verifier := newVerifier(t, identity, netsec.WithKeyUsages(x509.ExtKeyUsageServerAuth))

	t.Run("different exporter", func(t *testing.T) {
		request, err := verifier.IssueRequest()
		if err != nil {
			t.Fatal(err)
		}
		authFromA := buildAuthenticator(t, request, connectionExporter("connection-a"), identity)

		_, err = verifier.Verify(authFromA, request, connectionExporter("connection-b"))
		if !errors.Is(err, netsec.ErrSignature) {
			t.Fatalf("Verify() error = %v, want ErrSignature", err)
		}
	})

	t.Run("different request context", func(t *testing.T) {
		requestA, err := verifier.IssueRequest()
		if err != nil {
			t.Fatal(err)
		}
		requestB, err := verifier.IssueRequest()
		if err != nil {
			t.Fatal(err)
		}
		authFromA := buildAuthenticator(t, requestA, connectionExporter("connection-a"), identity)

		_, err = verifier.Verify(authFromA, requestB, connectionExporter("connection-b"))
		if !errors.Is(err, netsec.ErrRequestContextMismatch) {
			t.Fatalf("Verify() error = %v, want ErrRequestContextMismatch", err)
		}
	})
}

func TestMissingAndForeignRequestRejected(t *testing.T) {
	identity := newTestIdentity(t)
	verifier := newVerifier(t, identity)
	request, err := verifier.IssueRequest()
	if err != nil {
		t.Fatal(err)
	}
	exporter := connectionExporter("connection-a")
	authenticator := buildAuthenticator(t, request, exporter, identity)

	_, err = verifier.Verify(authenticator, netsec.Request{}, exporter)
	if !errors.Is(err, netsec.ErrRequestNotFound) {
		t.Fatalf("Verify(zero request) error = %v, want ErrRequestNotFound", err)
	}

	otherVerifier := newVerifier(t, identity)
	_, err = otherVerifier.Verify(authenticator, request, exporter)
	if !errors.Is(err, netsec.ErrRequestNotFound) {
		t.Fatalf("Verify(foreign request) error = %v, want ErrRequestNotFound", err)
	}
}

func TestRequestIsSingleUse(t *testing.T) {
	identity := newTestIdentity(t)
	verifier := newVerifier(t, identity)
	request, err := verifier.IssueRequest()
	if err != nil {
		t.Fatal(err)
	}
	exporter := connectionExporter("connection-a")
	authenticator := buildAuthenticator(t, request, exporter, identity)

	if _, err := verifier.Verify(authenticator, request, exporter); err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
	if _, err := verifier.Verify(authenticator, request, exporter); !errors.Is(err, netsec.ErrRequestUsed) {
		t.Fatalf("second Verify() error = %v, want ErrRequestUsed", err)
	}
}

func TestFailedAttemptConsumesRequest(t *testing.T) {
	identity := newTestIdentity(t)
	verifier := newVerifier(t, identity)
	request, err := verifier.IssueRequest()
	if err != nil {
		t.Fatal(err)
	}
	exporter := connectionExporter("connection-a")
	authenticator := buildAuthenticator(t, request, exporter, identity)
	tampered := cloneAndFlipLastByte(authenticator)

	if _, err := verifier.Verify(tampered, request, exporter); !errors.Is(err, netsec.ErrFinished) {
		t.Fatalf("tampered Verify() error = %v, want ErrFinished", err)
	}
	if _, err := verifier.Verify(authenticator, request, exporter); !errors.Is(err, netsec.ErrRequestUsed) {
		t.Fatalf("Verify() after failed attempt error = %v, want ErrRequestUsed", err)
	}
}

func TestConcurrentReplayAllowsExactlyOneSuccess(t *testing.T) {
	identity := newTestIdentity(t)
	verifier := newVerifier(t, identity)
	request, err := verifier.IssueRequest()
	if err != nil {
		t.Fatal(err)
	}
	exporter := connectionExporter("connection-a")
	authenticator := buildAuthenticator(t, request, exporter, identity)

	const workers = 16
	var successes atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := verifier.Verify(authenticator, request, exporter); err == nil {
				successes.Add(1)
			} else if errors.Is(err, netsec.ErrRequestUsed) {
				failures.Add(1)
			} else {
				t.Errorf("Verify() unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful verifications = %d, want 1", got)
	}
	if got := failures.Load(); got != workers-1 {
		t.Fatalf("single-use rejections = %d, want %d", got, workers-1)
	}
}

func TestMessageTamperingRejected(t *testing.T) {
	identity := newTestIdentity(t)

	t.Run("certificate verify signature", func(t *testing.T) {
		verifier := newVerifier(t, identity)
		request, err := verifier.IssueRequest()
		if err != nil {
			t.Fatal(err)
		}
		exporter := connectionExporter("connection-a")
		authenticator := buildAuthenticator(t, request, exporter, identity)
		tampered := cloneAndFlipCertificateVerifySignature(t, authenticator)

		if _, err := verifier.Verify(tampered, request, exporter); !errors.Is(err, netsec.ErrSignature) {
			t.Fatalf("Verify() error = %v, want ErrSignature", err)
		}
	})

	t.Run("finished", func(t *testing.T) {
		verifier := newVerifier(t, identity)
		request, err := verifier.IssueRequest()
		if err != nil {
			t.Fatal(err)
		}
		exporter := connectionExporter("connection-a")
		authenticator := buildAuthenticator(t, request, exporter, identity)
		tampered := cloneAndFlipLastByte(authenticator)

		if _, err := verifier.Verify(tampered, request, exporter); !errors.Is(err, netsec.ErrFinished) {
			t.Fatalf("Verify() error = %v, want ErrFinished", err)
		}
	})

	t.Run("certificate request context", func(t *testing.T) {
		verifier := newVerifier(t, identity)
		request, err := verifier.IssueRequest()
		if err != nil {
			t.Fatal(err)
		}
		exporter := connectionExporter("connection-a")
		authenticator := buildAuthenticator(t, request, exporter, identity)
		tampered := cloneAndFlipCertificateContext(t, authenticator)

		if _, err := verifier.Verify(tampered, request, exporter); !errors.Is(err, netsec.ErrRequestContextMismatch) {
			t.Fatalf("Verify() error = %v, want ErrRequestContextMismatch", err)
		}
	})
}

func TestExpiredRequestRejected(t *testing.T) {
	identity := newTestIdentity(t)
	now := time.Now()
	verifier := newVerifier(
		t,
		identity,
		netsec.WithClock(func() time.Time { return now }),
		netsec.WithRequestTTL(time.Second),
	)
	request, err := verifier.IssueRequest()
	if err != nil {
		t.Fatal(err)
	}
	exporter := connectionExporter("connection-a")
	authenticator := buildAuthenticator(t, request, exporter, identity)
	now = now.Add(2 * time.Second)

	if _, err := verifier.Verify(authenticator, request, exporter); !errors.Is(err, netsec.ErrRequestExpired) {
		t.Fatalf("Verify() error = %v, want ErrRequestExpired", err)
	}
}

func cloneAndFlipLastByte(input []byte) []byte {
	clone := append([]byte(nil), input...)
	clone[len(clone)-1] ^= 0x01
	return clone
}

func cloneAndFlipCertificateVerifySignature(t *testing.T, authenticator []byte) []byte {
	t.Helper()
	parsed, err := netsec.ParseAuthenticator(authenticator)
	if err != nil {
		t.Fatal(err)
	}
	offset := len(parsed.Certificate) + 4 + 2 + 2
	clone := append([]byte(nil), authenticator...)
	clone[offset+1] ^= 0x01
	return clone
}

func cloneAndFlipCertificateContext(t *testing.T, authenticator []byte) []byte {
	t.Helper()
	parsed, err := netsec.ParseAuthenticator(authenticator)
	if err != nil {
		t.Fatal(err)
	}
	_ = parsed
	clone := append([]byte(nil), authenticator...)
	clone[4+1] ^= 0x01
	return clone
}

func roleName(role netsec.Role) string {
	if role == netsec.RoleClient {
		return "client"
	}
	return "server"
}
