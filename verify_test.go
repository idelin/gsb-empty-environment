package netsec_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	netsec "example.org/netsec/q0049"
)

var (
	testKey     = []byte("test signing key")
	errBadSig   = errors.New("bad signature")
	testUpdates = []netsec.Update{{Old: oidA, New: oidB, Ref: "refs/heads/main"}}
)

func TestVerifyAcceptsMatchingSignedPush(t *testing.T) {
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, testUpdates, testKey)

	var signedPayload, signedBlock []byte
	verifier, err := netsec.NewVerifier(netsec.Config{
		VerifySignature: func(payload, signature []byte) error {
			signedPayload = append([]byte(nil), payload...)
			signedBlock = append([]byte(nil), signature...)
			return verifySignature(payload, signature, testKey)
		},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	cert, err := verifier.Verify(certificate, testUpdates)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if cert.Pusher != testPusher || cert.Pushee != testPushee || cert.Nonce != testNonce {
		t.Fatalf("Verify() returned certificate = %#v", cert)
	}
	if string(signedPayload) != certificatePayload(testPusher, testPushee, testNonce, testUpdates) {
		t.Fatal("signature callback received the wrong signed payload")
	}
	if !strings.HasPrefix(string(signedBlock), "-----BEGIN PGP SIGNATURE-----") {
		t.Fatal("signature callback did not receive the signature block")
	}
}

func TestVerifyRejectsReplayedNonce(t *testing.T) {
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, testUpdates, testKey)
	verifier := newTestVerifier(t, testKey, netsec.Config{})

	if _, err := verifier.Verify(certificate, testUpdates); err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
	if _, err := verifier.Verify(certificate, testUpdates); !errors.Is(err, netsec.ErrNonceReused) {
		t.Fatalf("replayed Verify() error = %v, want ErrNonceReused", err)
	}
}

func TestVerifyConsumesNonceAtomically(t *testing.T) {
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, testUpdates, testKey)
	verifier := newTestVerifier(t, testKey, netsec.Config{})

	const goroutines = 16
	results := make(chan error, goroutines)
	var wait sync.WaitGroup
	for range goroutines {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := verifier.Verify(certificate, testUpdates)
			results <- err
		}()
	}
	wait.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, netsec.ErrNonceReused):
		default:
			t.Errorf("concurrent Verify() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent Verify() successes = %d, want 1", successes)
	}
}

func TestVerifyRejectsUpdatesOutsideCertificate(t *testing.T) {
	certUpdates := []netsec.Update{
		{Old: oidA, New: oidB, Ref: "refs/heads/main"},
		{Old: oidC, New: zeroOID, Ref: "refs/heads/removed"},
	}
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, certUpdates, testKey)
	verifier := newTestVerifier(t, testKey, netsec.Config{})

	tests := []struct {
		name    string
		updates []netsec.Update
	}{
		{
			name: "extra ref created outside certificate",
			updates: []netsec.Update{
				{Old: oidA, New: oidB, Ref: "refs/heads/main"},
				{Old: oidC, New: zeroOID, Ref: "refs/heads/removed"},
				{Old: zeroOID, New: oidA, Ref: "refs/heads/injected"},
			},
		},
		{
			name: "extra ref deleted outside certificate",
			updates: []netsec.Update{
				{Old: oidA, New: oidB, Ref: "refs/heads/main"},
				{Old: oidC, New: zeroOID, Ref: "refs/heads/removed"},
				{Old: oidB, New: zeroOID, Ref: "refs/heads/victim"},
			},
		},
		{
			name: "certificate update hidden from hook",
			updates: []netsec.Update{
				{Old: oidA, New: oidB, Ref: "refs/heads/main"},
			},
		},
		{
			name: "same ref with different new object",
			updates: []netsec.Update{
				{Old: oidA, New: oidC, Ref: "refs/heads/main"},
				{Old: oidC, New: zeroOID, Ref: "refs/heads/removed"},
			},
		},
		{
			name: "reordered updates",
			updates: []netsec.Update{
				{Old: oidC, New: zeroOID, Ref: "refs/heads/removed"},
				{Old: oidA, New: oidB, Ref: "refs/heads/main"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := verifier.Verify(certificate, test.updates)
			if !errors.Is(err, netsec.ErrUpdateMismatch) {
				t.Fatalf("Verify() error = %v, want ErrUpdateMismatch", err)
			}
			var mismatch *netsec.UpdateMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("Verify() error type = %T, want *UpdateMismatchError", err)
			}
		})
	}
}

func TestVerifyProtectedRefsRequireAuthorizedPusher(t *testing.T) {
	const (
		alice   = "Alice Example <alice@example.com>"
		mallory = "Mallory <mallory@example.com>"
		bot     = "Release Bot <bot@example.com>"
	)
	config := netsec.Config{
		ProtectedRefs: map[string][]string{
			"refs/heads/main":      {alice},
			"refs/heads/release/*": {bot},
		},
	}
	verifier := newTestVerifier(t, testKey, config)

	mainUpdate := []netsec.Update{{Old: oidA, New: oidB, Ref: "refs/heads/main"}}
	mainDelete := []netsec.Update{{Old: oidB, New: zeroOID, Ref: "refs/heads/main"}}

	denied := buildCertificate(t, mallory, testPushee, "nonce-denied", mainUpdate, testKey)
	if _, err := verifier.Verify(denied, mainUpdate); !errors.Is(err, netsec.ErrUnauthorizedPusher) {
		t.Fatalf("unauthorized push error = %v, want ErrUnauthorizedPusher", err)
	}

	allowed := buildCertificate(t, alice, testPushee, testNonce, mainUpdate, testKey)
	if _, err := verifier.Verify(allowed, mainUpdate); err != nil {
		t.Fatalf("authorized push error = %v", err)
	}

	deniedDelete := buildCertificate(t, mallory, testPushee, "nonce-delete", mainDelete, testKey)
	if _, err := verifier.Verify(deniedDelete, mainDelete); !errors.Is(err, netsec.ErrUnauthorizedPusher) {
		t.Fatalf("unauthorized deletion error = %v, want ErrUnauthorizedPusher", err)
	}

	allowedDelete := buildCertificate(t, alice, testPushee, "nonce-delete-ok", mainDelete, testKey)
	if _, err := verifier.Verify(allowedDelete, mainDelete); err != nil {
		t.Fatalf("authorized deletion error = %v", err)
	}

	globUpdate := []netsec.Update{{Old: zeroOID, New: oidC, Ref: "refs/heads/release/1.2"}}
	globCert := buildCertificate(t, bot, testPushee, "nonce-glob", globUpdate, testKey)
	if _, err := verifier.Verify(globCert, globUpdate); err != nil {
		t.Fatalf("glob-authorized push error = %v", err)
	}

	featureUpdate := []netsec.Update{{Old: zeroOID, New: oidC, Ref: "refs/heads/feature"}}
	featureCert := buildCertificate(t, mallory, testPushee, "nonce-feature", featureUpdate, testKey)
	if _, err := verifier.Verify(featureCert, featureUpdate); err != nil {
		t.Fatalf("unprotected push error = %v", err)
	}
}

func TestVerifyDoesNotConsumeNonceForDeniedPusher(t *testing.T) {
	config := netsec.Config{
		ProtectedRefs: map[string][]string{
			"refs/heads/main": {"Alice <alice@example.com>"},
		},
	}
	verifier := newTestVerifier(t, testKey, config)

	denied := buildCertificate(t, "Mallory <mallory@example.com>", testPushee,
		"nonce-still-free", testUpdates, testKey)
	if _, err := verifier.Verify(denied, testUpdates); !errors.Is(err, netsec.ErrUnauthorizedPusher) {
		t.Fatalf("denied push error = %v, want ErrUnauthorizedPusher", err)
	}

	allowed := buildCertificate(t, "Alice <alice@example.com>", testPushee,
		"nonce-still-free", testUpdates, testKey)
	if _, err := verifier.Verify(allowed, testUpdates); err != nil {
		t.Fatalf("authorized push with same nonce error = %v", err)
	}
}

func TestVerifyRejectsSignatureFailure(t *testing.T) {
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, testUpdates, []byte("wrong key"))
	verifier := newTestVerifier(t, testKey, netsec.Config{})

	_, err := verifier.Verify(certificate, testUpdates)
	if !errors.Is(err, netsec.ErrSignatureVerification) {
		t.Fatalf("Verify() error = %v, want ErrSignatureVerification", err)
	}
	if !errors.Is(err, errBadSig) {
		t.Fatalf("Verify() error = %v, want callback error to be wrapped", err)
	}
}

func TestVerifyRejectsPusheeMismatch(t *testing.T) {
	certificate := buildCertificate(t, testPusher, "ssh://git.example.org/other.git",
		testNonce, testUpdates, testKey)
	verifier := newTestVerifier(t, testKey, netsec.Config{Pushee: testPushee})

	_, err := verifier.Verify(certificate, testUpdates)
	if !errors.Is(err, netsec.ErrPusheeMismatch) {
		t.Fatalf("Verify() error = %v, want ErrPusheeMismatch", err)
	}
}

func TestVerifyReader(t *testing.T) {
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, testUpdates, testKey)
	verifier := newTestVerifier(t, testKey, netsec.Config{})

	if _, err := verifier.VerifyReader(certificate, strings.NewReader(testUpdates[0].String()+"\n")); err != nil {
		t.Fatalf("VerifyReader() error = %v", err)
	}
}

func TestVerifyNonceStoreFailure(t *testing.T) {
	certificate := buildCertificate(t, testPusher, testPushee, testNonce, testUpdates, testKey)
	verifier, err := netsec.NewVerifier(netsec.Config{
		VerifySignature: verifySignatureFunc(testKey),
		Nonces:          failingNonceStore{},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	if _, err := verifier.Verify(certificate, testUpdates); !errors.Is(err, netsec.ErrNonceStore) {
		t.Fatalf("Verify() error = %v, want ErrNonceStore", err)
	}
}

func TestNewVerifierRequiresSignatureCallback(t *testing.T) {
	if _, err := netsec.NewVerifier(netsec.Config{}); !errors.Is(err, netsec.ErrInvalidConfig) {
		t.Fatalf("NewVerifier() error = %v, want ErrInvalidConfig", err)
	}
}

type failingNonceStore struct{}

func (failingNonceStore) Consume(string) (bool, error) {
	return false, errors.New("store unavailable")
}

func newTestVerifier(t *testing.T, key []byte, config netsec.Config) *netsec.Verifier {
	t.Helper()
	config.VerifySignature = verifySignatureFunc(key)
	verifier, err := netsec.NewVerifier(config)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	return verifier
}

func verifySignatureFunc(key []byte) netsec.SignatureVerifier {
	return func(payload, signature []byte) error {
		return verifySignature(payload, signature, key)
	}
}

func verifySignature(payload, signature, key []byte) error {
	if !hmac.Equal(signature, signPayload(payload, key)) {
		return errBadSig
	}
	return nil
}

func buildCertificate(t *testing.T, pusher, pushee, nonce string, updates []netsec.Update, key []byte) []byte {
	t.Helper()
	payload := certificatePayload(pusher, pushee, nonce, updates)
	return append([]byte(payload), signPayload([]byte(payload), key)...)
}

func certificatePayload(pusher, pushee, nonce string, updates []netsec.Update) string {
	var payload strings.Builder
	payload.WriteString("certificate version 0.1\n")
	payload.WriteString("pusher " + pusher + "\n")
	payload.WriteString("pushee " + pushee + "\n")
	payload.WriteString("nonce " + nonce + "\n")
	for _, update := range updates {
		payload.WriteString(update.String() + "\n")
	}
	return payload.String()
}

func signPayload(payload, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	sum := hex.EncodeToString(mac.Sum(nil))
	return []byte("-----BEGIN PGP SIGNATURE-----\n" + sum + "\n-----END PGP SIGNATURE-----\n")
}
