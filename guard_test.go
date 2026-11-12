package netsec

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testIdentity struct {
	certificate []byte
	key         *rsa.PrivateKey
}

func newTestIdentity(t *testing.T, commonName string, serial int64) testIdentity {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return testIdentity{certificate: der, key: key}
}

func (identity testIdentity) signPKCS1SHA256(t *testing.T, data []byte) []byte {
	return identity.signPKCS1(t, crypto.SHA256, data)
}

func (identity testIdentity) signPKCS1(t *testing.T, hash crypto.Hash, data []byte) []byte {
	t.Helper()

	h := hash.New()
	if _, err := h.Write(data); err != nil {
		t.Fatalf("hash data: %v", err)
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, identity.key, hash, h.Sum(nil))
	if err != nil {
		t.Fatalf("sign data: %v", err)
	}
	return signature
}

func baseEndpoint(serverCertificate []byte) Endpoint {
	return Endpoint{
		SecurityPolicyURI: SecurityPolicyBasic256Sha256,
		ServerCertificate: serverCertificate,
		UserTokenPolicies: []UserTokenPolicy{
			{
				PolicyID:          "username",
				TokenType:         TokenTypeUserName,
				SecurityPolicyURI: SecurityPolicyBasic256Sha256,
			},
			{
				PolicyID:          "certificate",
				TokenType:         TokenTypeCertificate,
				SecurityPolicyURI: SecurityPolicyBasic256Sha256,
			},
		},
	}
}

func TestUserNameTokenRejectsPlaintext(t *testing.T) {
	tests := []struct {
		name                string
		channelPolicy       string
		tokenPolicy         string
		encryptionAlgorithm string
	}{
		{
			name:                "both empty",
			channelPolicy:       "",
			tokenPolicy:         "",
			encryptionAlgorithm: "",
		},
		{
			name:                "both explicit None",
			channelPolicy:       SecurityPolicyNone,
			tokenPolicy:         SecurityPolicyNone,
			encryptionAlgorithm: SecurityPolicyNone,
		},
		{
			name:                "policy inherits unencrypted channel",
			channelPolicy:       "None",
			tokenPolicy:         "",
			encryptionAlgorithm: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := Endpoint{
				SecurityPolicyURI: test.channelPolicy,
				UserTokenPolicies: []UserTokenPolicy{{
					PolicyID:          "username",
					TokenType:         TokenTypeUserName,
					SecurityPolicyURI: test.tokenPolicy,
				}},
			}
			request := ActivateSessionRequest{
				PolicyID:            "username",
				UserName:            "alice",
				Password:            []byte("secret"),
				EncryptionAlgorithm: test.encryptionAlgorithm,
			}

			err := NewGuard(endpoint).ActivateSession(request)
			if !errors.Is(err, ErrPlaintextPassword) {
				t.Fatalf("expected ErrPlaintextPassword, got %v", err)
			}
		})
	}
}

func TestUserNameTokenRejectsEncryptionMismatch(t *testing.T) {
	endpoint := Endpoint{
		SecurityPolicyURI: SecurityPolicyBasic256Sha256,
		UserTokenPolicies: []UserTokenPolicy{{
			PolicyID:  "username",
			TokenType: TokenTypeUserName,
		}},
	}

	for _, algorithm := range []string{"", SecurityPolicyNone, SecurityPolicyBasic256} {
		err := ValidateUserNameToken(endpoint, "username", algorithm)
		if !errors.Is(err, ErrEncryptionAlgorithmMismatch) {
			t.Fatalf("algorithm %q: expected ErrEncryptionAlgorithmMismatch, got %v", algorithm, err)
		}
	}
}

func TestUserNameTokenAcceptsEncryptedPassword(t *testing.T) {
	endpoint := Endpoint{
		SecurityPolicyURI: SecurityPolicyBasic256Sha256,
		UserTokenPolicies: []UserTokenPolicy{{
			PolicyID:  "username",
			TokenType: TokenTypeUserName,
		}},
	}
	request := ActivateSessionRequest{
		UserName:            "alice",
		Password:            []byte("ciphertext"),
		EncryptionAlgorithm: SecurityPolicyBasic256Sha256,
		PolicyID:            "username",
	}
	if err := NewGuard(endpoint).ActivateSession(request); err != nil {
		t.Fatalf("encrypted password token rejected: %v", err)
	}
}

func TestUndeclaredPolicyIDIsRejected(t *testing.T) {
	endpoint := baseEndpoint([]byte("server certificate"))

	userRequest := ActivateSessionRequest{
		PolicyID:            "made-up-policy",
		TokenType:           TokenTypeUserName,
		Password:            []byte("ciphertext"),
		EncryptionAlgorithm: SecurityPolicyBasic256Sha256,
	}
	if err := NewGuard(endpoint).ActivateSession(userRequest); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("username token: expected ErrPolicyNotFound, got %v", err)
	}

	certificateRequest := ActivateSessionRequest{
		PolicyID:  "made-up-policy",
		TokenType: TokenTypeCertificate,
	}
	if err := NewGuard(endpoint).ActivateSession(certificateRequest); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("certificate token: expected ErrPolicyNotFound, got %v", err)
	}
}

func TestPolicyIDMustMatchTokenType(t *testing.T) {
	endpoint := baseEndpoint([]byte("server certificate"))
	request := ActivateSessionRequest{
		PolicyID:  "username",
		TokenType: TokenTypeCertificate,
	}
	if err := NewGuard(endpoint).ActivateSession(request); !errors.Is(err, ErrTokenTypeMismatch) {
		t.Fatalf("expected ErrTokenTypeMismatch, got %v", err)
	}
}

func TestConflictingDuplicatePolicyIDsAreRejected(t *testing.T) {
	endpoint := Endpoint{
		SecurityPolicyURI: SecurityPolicyBasic256Sha256,
		UserTokenPolicies: []UserTokenPolicy{
			{PolicyID: "duplicate", TokenType: TokenTypeUserName, SecurityPolicyURI: SecurityPolicyBasic256Sha256},
			{PolicyID: "duplicate", TokenType: TokenTypeUserName, SecurityPolicyURI: SecurityPolicyNone},
		},
	}
	request := ActivateSessionRequest{
		PolicyID:            "duplicate",
		EncryptionAlgorithm: SecurityPolicyBasic256Sha256,
	}
	err := NewGuard(endpoint).ActivateSession(request)
	if !errors.Is(err, ErrPolicyAmbiguous) {
		t.Fatalf("expected ErrPolicyAmbiguous, got %v", err)
	}
}

func TestCertificateTokenAcceptedThenNonceReuseRejected(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	server := newTestIdentity(t, "server", 2)
	endpoint := baseEndpoint(server.certificate)
	nonce := []byte("0123456789abcdef")

	request := ActivateSessionRequest{
		PolicyID:          "certificate",
		TokenType:         TokenTypeCertificate,
		Certificate:       client.certificate,
		ClientCertificate: client.certificate,
		Signature:         client.signPKCS1SHA256(t, SignedData(server.certificate, nonce)),
		ServerNonce:       nonce,
	}
	guard := NewGuard(endpoint)

	if err := guard.ActivateSession(request); err != nil {
		t.Fatalf("valid certificate token rejected: %v", err)
	}
	if err := guard.ActivateSession(request); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("expected ErrNonceReused on replay, got %v", err)
	}
}

func TestCertificateNonceReuseRejectedAcrossCertificates(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	otherClient := newTestIdentity(t, "other-client", 3)
	server := newTestIdentity(t, "server", 2)
	endpoint := baseEndpoint(server.certificate)
	nonce := []byte("same-nonce-for-two-activations")
	guard := NewGuard(endpoint)

	first := ActivateSessionRequest{
		PolicyID:    "certificate",
		Certificate: client.certificate,
		Signature:   client.signPKCS1SHA256(t, SignedData(server.certificate, nonce)),
		ServerNonce: nonce,
	}
	if err := guard.ActivateSession(first); err != nil {
		t.Fatalf("first activation rejected: %v", err)
	}

	second := ActivateSessionRequest{
		PolicyID:    "certificate",
		Certificate: otherClient.certificate,
		Signature:   otherClient.signPKCS1SHA256(t, SignedData(server.certificate, nonce)),
		ServerNonce: nonce,
	}
	if err := guard.ActivateSession(second); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("expected ErrNonceReused for same nonce with another certificate, got %v", err)
	}
}

func TestCertificateSignatureMustCoverBothServerCertificateAndNonce(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	server := newTestIdentity(t, "server", 2)
	endpoint := baseEndpoint(server.certificate)
	nonce := []byte("nonce-that-must-be-covered")

	tests := []struct {
		name string
		data []byte
	}{
		{name: "only server certificate", data: server.certificate},
		{name: "only nonce", data: nonce},
		{name: "nonce before server certificate", data: SignedData(nonce, server.certificate)},
		{
			name: "separator inserted between server certificate and nonce",
			data: append(append(append([]byte{}, server.certificate...), 0), nonce...),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := ActivateSessionRequest{
				PolicyID:    "certificate",
				Certificate: client.certificate,
				Signature:   client.signPKCS1SHA256(t, test.data),
				ServerNonce: nonce,
			}
			err := NewGuard(endpoint).ActivateSession(request)
			if !errors.Is(err, ErrSignatureInvalid) {
				t.Fatalf("expected ErrSignatureInvalid, got %v", err)
			}
		})
	}
}

func TestCertificateSignatureAlgorithmMustMatchPolicy(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	server := newTestIdentity(t, "server", 2)
	endpoint := baseEndpoint(server.certificate)
	nonce := []byte("nonce")

	request := ActivateSessionRequest{
		PolicyID:           "certificate",
		Certificate:        client.certificate,
		SignatureAlgorithm: "rsa-sha1",
		Signature:          client.signPKCS1SHA256(t, SignedData(server.certificate, nonce)),
		ServerNonce:        nonce,
	}
	err := NewGuard(endpoint).ActivateSession(request)
	if !errors.Is(err, ErrSignatureAlgorithmMismatch) {
		t.Fatalf("expected ErrSignatureAlgorithmMismatch, got %v", err)
	}
}

func TestCertificateTokenFallsBackToChannelPolicy(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	server := newTestIdentity(t, "server", 2)
	endpoint := Endpoint{
		SecurityPolicyURI: SecurityPolicyBasic256Sha256,
		ServerCertificate: server.certificate,
		UserTokenPolicies: []UserTokenPolicy{{
			PolicyID:  "certificate",
			TokenType: TokenTypeCertificate,
		}},
	}
	nonce := []byte("nonce")
	request := ActivateSessionRequest{
		PolicyID:    "certificate",
		Certificate: client.certificate,
		Signature:   client.signPKCS1SHA256(t, SignedData(server.certificate, nonce)),
		ServerNonce: nonce,
	}
	if err := NewGuard(endpoint).ActivateSession(request); err != nil {
		t.Fatalf("certificate token with inherited channel policy rejected: %v", err)
	}
}

func TestCertificateTokenBasic256DerivesSHA1(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	server := newTestIdentity(t, "server", 2)
	endpoint := Endpoint{
		SecurityPolicyURI: SecurityPolicyBasic256,
		ServerCertificate: server.certificate,
		UserTokenPolicies: []UserTokenPolicy{{
			PolicyID:  "certificate",
			TokenType: TokenTypeCertificate,
		}},
	}
	nonce := []byte("nonce")
	request := ActivateSessionRequest{
		PolicyID:    "certificate",
		Certificate: client.certificate,
		Signature:   client.signPKCS1(t, crypto.SHA1, SignedData(server.certificate, nonce)),
		ServerNonce: nonce,
	}
	if err := NewGuard(endpoint).ActivateSession(request); err != nil {
		t.Fatalf("Basic256 certificate token rejected: %v", err)
	}
}

func TestCertificateSessionBindingAndFieldsAreRequired(t *testing.T) {
	client := newTestIdentity(t, "client", 1)
	other := newTestIdentity(t, "other", 2)
	server := newTestIdentity(t, "server", 3)
	endpoint := baseEndpoint(server.certificate)
	nonce := []byte("nonce")
	validSignature := client.signPKCS1SHA256(t, SignedData(server.certificate, nonce))

	request := ActivateSessionRequest{
		PolicyID:          "certificate",
		Certificate:       client.certificate,
		ClientCertificate: other.certificate,
		Signature:         validSignature,
		ServerNonce:       nonce,
	}
	if err := NewGuard(endpoint).ActivateSession(request); !errors.Is(err, ErrCertificateMismatch) {
		t.Fatalf("expected ErrCertificateMismatch, got %v", err)
	}

	request.ClientCertificate = client.certificate
	request.ServerNonce = nil
	if err := NewGuard(endpoint).ActivateSession(request); !errors.Is(err, ErrNonceMissing) {
		t.Fatalf("expected ErrNonceMissing, got %v", err)
	}
}

func TestNonceStoreConsumesExactlyOnceConcurrently(t *testing.T) {
	store := NewNonceStore()
	nonce := []byte("shared-nonce")
	var successes int32
	var wait sync.WaitGroup

	for i := 0; i < 50; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if store.Consume(nonce) {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	wait.Wait()

	if successes != 1 {
		t.Fatalf("nonce consumed %d times, want exactly 1", successes)
	}
	if !store.IsUsed(nonce) {
		t.Fatal("nonce should be marked as used")
	}
	if store.Len() != 1 {
		t.Fatalf("store length = %d, want 1", store.Len())
	}
}
