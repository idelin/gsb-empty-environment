package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestContextPrefixIsExactly64Spaces(t *testing.T) {
	tests := []struct {
		name    string
		context string
		label   string
	}{
		{"server", ServerContext, ServerContextLabel},
		{"client", ClientContext, ClientContextLabel},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(test.context); got != ContextPrefixLength+len(test.label) {
				t.Fatalf("context length = %d, want %d", got, ContextPrefixLength+len(test.label))
			}
			if !bytes.Equal([]byte(test.context[:ContextPrefixLength]), bytes.Repeat([]byte{0x20}, ContextPrefixLength)) {
				t.Fatalf("context prefix is not 64 0x20 octets")
			}
			if got := test.context[ContextPrefixLength:]; got != test.label {
				t.Fatalf("context label = %q, want %q", got, test.label)
			}
		})
	}
}

func TestSignedMessageLayout(t *testing.T) {
	transcript := sha256.Sum256([]byte("transcript"))
	message, err := SignedMessage(RoleServer, transcript[:])
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(message[:ContextPrefixLength], bytes.Repeat([]byte{0x20}, ContextPrefixLength)) {
		t.Fatal("signed message does not start with 64 spaces")
	}
	if !bytes.HasPrefix(message[ContextPrefixLength:], []byte(ServerContextLabel+"\x00")) {
		t.Fatal("signed message context or separator is wrong")
	}
	if !bytes.HasSuffix(message, transcript[:]) {
		t.Fatal("signed message does not end with the transcript hash")
	}

	role, gotTranscript, err := SplitSignedMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	if role != RoleServer || !bytes.Equal(gotTranscript, transcript[:]) {
		t.Fatalf("SplitSignedMessage = (%q, %x), want (%q, %x)", role, gotTranscript, RoleServer, transcript)
	}
}

func TestSignAndVerifyCertificateVerify(t *testing.T) {
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, ed25519Key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	transcript := sha256.Sum256([]byte("TLS 1.3 transcript"))
	tests := []struct {
		name   string
		priv   crypto.Signer
		scheme SignatureScheme
	}{
		{"ECDSA P-256", ecdsaKey, ECDSAWithP256AndSHA256},
		{"Ed25519", ed25519Key, Ed25519},
		{"RSA-PSS", rsaKey, PSSWithSHA256},
		{"RSA-PKCS1", rsaKey, PKCS1WithSHA256},
	}

	for _, test := range tests {
		for _, role := range []Role{RoleServer, RoleClient} {
			t.Run(test.name+"/"+role, func(t *testing.T) {
				signOptions := &SignOptions{Scheme: test.scheme}
				signature, err := SignCertificateVerify(test.priv, role, transcript[:], signOptions)
				if err != nil {
					t.Fatal(err)
				}

				verifyOptions := &VerifyOptions{Scheme: test.scheme}
				if err := VerifyCertificateVerify(test.priv.Public(), role, transcript[:], signature, verifyOptions); err != nil {
					t.Fatalf("valid signature rejected: %v", err)
				}

				wrongRole := RoleClient
				if role == RoleClient {
					wrongRole = RoleServer
				}
				if err := VerifyCertificateVerify(test.priv.Public(), wrongRole, transcript[:], signature, verifyOptions); err == nil {
					t.Fatal("signature accepted for the wrong role")
				}

				tampered := append([]byte(nil), signature...)
				tampered[len(tampered)-1] ^= 0x01
				if err := VerifyCertificateVerify(test.priv.Public(), role, transcript[:], tampered, verifyOptions); err == nil {
					t.Fatal("tampered signature accepted")
				}
			})
		}
	}
}

func TestOneMissingSpaceIsRejected(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transcript := sha256.Sum256([]byte("TLS 1.3 transcript"))
	opts := &VerifyOptions{Scheme: ECDSAWithP256AndSHA256}

	// A valid signature over the complete context is accepted.
	goodMessage, err := SignedMessage(RoleServer, transcript[:])
	if err != nil {
		t.Fatal(err)
	}
	goodSignature, err := SignSignedMessage(priv, goodMessage, &SignOptions{Scheme: ECDSAWithP256AndSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedMessage(priv.Public(), goodMessage, goodSignature, opts); err != nil {
		t.Fatalf("complete context rejected: %v", err)
	}

	// Remove exactly one leading 0x20 and sign the resulting malformed
	// message. The signature is cryptographically valid over that input, but
	// it must still be rejected because the RFC 8446 context is wrong.
	badContext := append([]byte(nil), []byte(ServerContext)[:ContextPrefixLength-1]...)
	badContext = append(badContext, []byte(ServerContextLabel)...)
	if err := ValidateContext(badContext); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("63-space context validation error = %v, want ErrInvalidContext", err)
	}

	badMessage := append([]byte(nil), badContext...)
	badMessage = append(badMessage, 0)
	badMessage = append(badMessage, transcript[:]...)
	badSignature, err := SignSignature(priv, 0, badMessage, &SignOptions{Scheme: ECDSAWithP256AndSHA256})
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifySignedMessage(priv.Public(), badMessage, badSignature, opts); !errors.Is(err, ErrInvalidSignedMessage) {
		t.Fatalf("63-space signed message error = %v, want ErrInvalidSignedMessage", err)
	}
	if err := Verify(priv.Public(), badMessage, badSignature, opts); err == nil {
		t.Fatal("Verify accepted a signed message with 63 spaces")
	}
	if err := VerifyContext(priv.Public(), badContext, transcript[:], badSignature, opts); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("63-space context error = %v, want ErrInvalidContext", err)
	}

	// Deleting one space from a context must fail even if the signature was
	// made over the original, complete context.
	truncatedGoodContext := []byte(ServerContext)[1:]
	if err := VerifyContext(priv.Public(), truncatedGoodContext, transcript[:], goodSignature, opts); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("truncated context with valid signature error = %v, want ErrInvalidContext", err)
	}
}

func TestLabelWithoutPrefixIsRejected(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transcript := sha256.Sum256([]byte("TLS 1.3 transcript"))
	opts := &VerifyOptions{Scheme: ECDSAWithP256AndSHA256}

	labelOnlyMessage := append([]byte(ServerContextLabel), 0)
	labelOnlyMessage = append(labelOnlyMessage, transcript[:]...)
	signature, err := SignSignature(priv, 0, labelOnlyMessage, &SignOptions{Scheme: ECDSAWithP256AndSHA256})
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidateSignedMessage(labelOnlyMessage); !errors.Is(err, ErrInvalidSignedMessage) {
		t.Fatalf("label-only message error = %v, want ErrInvalidSignedMessage", err)
	}
	if err := Verify(priv.Public(), ServerContextLabel, transcript[:], signature, opts); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("label-only context error = %v, want ErrInvalidContext", err)
	}
}

func TestVerifyDispatcherForms(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transcript := sha256.Sum256([]byte("TLS 1.3 transcript"))
	opts := &VerifyOptions{Scheme: ECDSAWithP256AndSHA256}
	signature, err := SignCertificateVerify(priv, RoleServer, transcript[:], &SignOptions{Scheme: ECDSAWithP256AndSHA256})
	if err != nil {
		t.Fatal(err)
	}
	message, err := SignedMessage(RoleServer, transcript[:])
	if err != nil {
		t.Fatal(err)
	}

	forms := [][]any{
		{priv.Public(), RoleServer, transcript[:], signature, opts},
		{RoleServer, transcript[:], signature, priv.Public(), opts},
		{priv.Public(), ServerContext, transcript[:], signature, opts},
		{ServerContext, transcript[:], signature, priv.Public(), opts},
		{priv.Public(), message, signature, opts},
		{message, signature, priv.Public(), opts},
		{priv.Public(), crypto.SHA256, message, signature, opts},
	}
	for i, form := range forms {
		if err := Verify(form...); err != nil {
			t.Errorf("Verify form %d rejected a valid signature: %v", i, err)
		}
	}
}

func TestSignDispatcher(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	transcript := sha256.Sum256([]byte("TLS 1.3 transcript"))
	opts := &SignOptions{Scheme: ECDSAWithP256AndSHA256}

	signature, err := Sign(priv, RoleClient, transcript[:], opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificateVerify(priv.Public(), RoleClient, transcript[:], signature, &VerifyOptions{Scheme: ECDSAWithP256AndSHA256}); err != nil {
		t.Fatalf("dispatcher signature rejected: %v", err)
	}
}
