package credssp

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy source unavailable")
}

type failingEncrypt struct {
	mockCrypto
}

func (failingEncrypt) Encrypt([]byte) ([]byte, error) {
	return nil, errors.New("session key unavailable")
}

type failOnSecondEncrypt struct {
	mockCrypto
	calls int
}

func (f *failOnSecondEncrypt) Encrypt(plaintext []byte) ([]byte, error) {
	f.calls++
	if f.calls == 2 {
		return nil, errors.New("session key unavailable")
	}
	return f.mockCrypto.Encrypt(plaintext)
}

// mockCrypto is a deterministic stand-in for the real CredSSP session crypto.
// Encrypt prefixes a marker and Decrypt removes it, so tests prove that every
// blob really traverses both callbacks; Hash is the standard SHA-256 required
// by MS-CSSP version 5.
type mockCrypto struct {
	decryptErr error
}

const encryptedPrefix = "enc:"

func (mockCrypto) Encrypt(plaintext []byte) ([]byte, error) {
	blob := make([]byte, 0, len(encryptedPrefix)+len(plaintext))
	blob = append(blob, encryptedPrefix...)
	blob = append(blob, plaintext...)
	return blob, nil
}

func (m mockCrypto) Decrypt(blob []byte) ([]byte, error) {
	if m.decryptErr != nil {
		return nil, m.decryptErr
	}
	if !bytes.HasPrefix(blob, []byte(encryptedPrefix)) {
		return nil, errors.New("missing encryption prefix")
	}
	return append([]byte(nil), blob[len(encryptedPrefix):]...), nil
}

func (mockCrypto) Hash(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

var testPublicKey = []byte{
	0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86,
	0x48, 0xce, 0x3d, 0x02, 0x01, 0x06, 0x08, 0x2a,
	0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03,
	0x42, 0x00, 0x04, 0xde, 0xad, 0xbe, 0xef,
}

func newHandshake(t *testing.T, cfg *Config) *Handshake {
	t.Helper()
	if cfg == nil {
		cfg = &Config{Crypto: mockCrypto{}}
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	return h
}

func clientToServerDigest(nonce, publicKey []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("CredSSP Client-To-Server Binding Hash")
	buf.WriteByte(0x00)
	buf.Write(nonce)
	buf.Write(publicKey)
	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

func serverToClientDigest(nonce, publicKey []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("CredSSP Server-To-Client Binding Hash")
	buf.WriteByte(0x00)
	buf.Write(nonce)
	buf.Write(publicKey)
	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

func legacyProof(publicKey []byte) []byte {
	proof := append([]byte(nil), publicKey...)
	proof[0]++
	return proof
}

// seal mirrors the mockCrypto.Encrypt wire format for building server blobs
// inside tests.
func seal(plaintext []byte) []byte {
	blob := make([]byte, 0, len(encryptedPrefix)+len(plaintext))
	blob = append(blob, encryptedPrefix...)
	blob = append(blob, plaintext...)
	return blob
}

// A man-in-the-middle that forces the server reply down to version 2 must be
// rejected by the default minimum-version policy before any binding or
// credential is produced.
func TestDowngradeToV2Rejected(t *testing.T) {
	h := newHandshake(t, nil)

	err := h.Negotiate(Version2)
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("Negotiate(2) error = %v, want ErrDowngrade", err)
	}
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted", h.State())
	}
	if h.Version() != 0 {
		t.Fatalf("Version() = %d, want 0 after abort", h.Version())
	}
	if _, err := h.ClientBinding(testPublicKey); !errors.Is(err, ErrAborted) {
		t.Fatalf("ClientBinding after downgrade error = %v, want ErrAborted", err)
	}
	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrAborted) {
		t.Fatalf("SealCredential after downgrade error = %v, want ErrAborted", err)
	}
}

func TestNegotiationTakesMinVersion(t *testing.T) {
	h := newHandshake(t, &Config{Crypto: mockCrypto{}})
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate(5): %v", err)
	}
	if got := h.Version(); got != Version5 {
		t.Fatalf("Version() = %d, want 5", got)
	}

	// Client offering at most 4 negotiates down to 4 against a v5 server when
	// the caller's minimum policy allows it.
	h = newHandshake(t, &Config{
		Crypto:     mockCrypto{},
		MaxVersion: Version4,
		MinVersion: Version2,
	})
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate(5) with client max 4: %v", err)
	}
	if got := h.Version(); got != Version4 {
		t.Fatalf("Version() = %d, want min(4,5)=4", got)
	}

	// Versions outside the supported range abort rather than being clamped.
	for _, bad := range []int{0, 1, 6, 99} {
		h = newHandshake(t, &Config{Crypto: mockCrypto{}, MinVersion: Version2})
		if err := h.Negotiate(bad); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("Negotiate(%d) error = %v, want ErrUnsupportedVersion", bad, err)
		}
	}
}

func TestV5BindingAndServerVerification(t *testing.T) {
	h := newHandshake(t, nil)
	nonce := h.ClientNonce()
	if len(nonce) != NonceSize {
		t.Fatalf("nonce length = %d, want %d", len(nonce), NonceSize)
	}

	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	content, err := h.ClientBinding(testPublicKey)
	if err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}
	contentPlain, err := mockCrypto{}.Decrypt(content)
	if err != nil {
		t.Fatalf("client binding not encrypted via callback: %v", err)
	}
	wantContent := clientToServerDigest(nonce, testPublicKey)
	if !bytes.Equal(contentPlain, wantContent) {
		t.Fatalf("client binding = %x, want %x", contentPlain, wantContent)
	}

	serverProof := seal(serverToClientDigest(nonce, testPublicKey))
	if err := h.VerifyServerBinding(Version5, serverProof); err != nil {
		t.Fatalf("VerifyServerBinding: %v", err)
	}
	if h.State() != StateVerified {
		t.Fatalf("state = %v, want StateVerified", h.State())
	}

	blob, err := h.SealCredential([]byte("domain\\user:password"))
	if err != nil {
		t.Fatalf("SealCredential: %v", err)
	}
	plain, err := mockCrypto{}.Decrypt(blob)
	if err != nil {
		t.Fatalf("sealed credential not encrypted via callback: %v", err)
	}
	if string(plain) != "domain\\user:password" {
		t.Fatalf("sealed credential = %q, want credential plaintext after decrypt", plain)
	}
}

// A man-in-the-middle that swaps the server public key after negotiation
// cannot produce a matching proof: verification fails, the handshake aborts
// and no credential blob is ever produced.
func TestTamperedPublicKeyAborts(t *testing.T) {
	h := newHandshake(t, nil)
	nonce := h.ClientNonce()
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}

	attackerKey := append([]byte(nil), testPublicKey...)
	attackerKey[len(attackerKey)-1] ^= 0x01
	forgedProof := seal(serverToClientDigest(nonce, attackerKey))

	err := h.VerifyServerBinding(Version5, forgedProof)
	if !errors.Is(err, ErrMismatch) {
		t.Fatalf("VerifyServerBinding error = %v, want ErrMismatch", err)
	}
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted", h.State())
	}
	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrAborted) {
		t.Fatalf("SealCredential error = %v, want ErrAborted", err)
	}
}

// Every new handshake must draw a fresh 32-byte nonce, and a replayed
// version-5 binding from an earlier handshake therefore cannot validate.
func TestClientNonceFreshPerHandshake(t *testing.T) {
	first := newHandshake(t, nil)
	second := newHandshake(t, nil)

	n1 := first.ClientNonce()
	n2 := second.ClientNonce()
	if bytes.Equal(n1, n2) {
		t.Fatal("client nonce reused across handshakes")
	}

	if err := first.Negotiate(Version5); err != nil {
		t.Fatalf("first Negotiate: %v", err)
	}
	replayed, err := first.ClientBinding(testPublicKey)
	if err != nil {
		t.Fatalf("first ClientBinding: %v", err)
	}

	if err := second.Negotiate(Version5); err != nil {
		t.Fatalf("second Negotiate: %v", err)
	}
	fresh, err := second.ClientBinding(testPublicKey)
	if err != nil {
		t.Fatalf("second ClientBinding: %v", err)
	}
	if bytes.Equal(replayed, fresh) {
		t.Fatal("version 5 binding identical across handshakes; nonce not bound")
	}
}

func TestServerProofMismatchAborts(t *testing.T) {
	h := newHandshake(t, nil)
	nonce := h.ClientNonce()
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}

	corrupt := serverToClientDigest(nonce, testPublicKey)
	corrupt[0] ^= 0xff
	err := h.VerifyServerBinding(Version5, seal(corrupt))
	if !errors.Is(err, ErrMismatch) {
		t.Fatalf("VerifyServerBinding error = %v, want ErrMismatch", err)
	}
	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrAborted) {
		t.Fatalf("SealCredential error = %v, want ErrAborted", err)
	}

	// A decrypt failure is treated the same way: abort, never verify.
	h = newHandshake(t, &Config{Crypto: mockCrypto{
		decryptErr: errors.New("session key failure"),
	}})
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}
	if err := h.VerifyServerBinding(Version5, seal([]byte("blob"))); !errors.Is(err, ErrMismatch) {
		t.Fatalf("VerifyServerBinding decrypt error = %v, want ErrMismatch", err)
	}
}

func TestVersionChangeMidHandshakeAborts(t *testing.T) {
	h := newHandshake(t, nil)
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}

	// Explicit check on an intermediate server TSRequest.
	if err := h.CheckServerVersion(Version4); !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("CheckServerVersion(4) error = %v, want ErrVersionChanged", err)
	}
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted", h.State())
	}

	// The same guard also applies inside proof verification: a fresh
	// handshake whose proof TSRequest carries a different version aborts.
	h = newHandshake(t, nil)
	nonce := h.ClientNonce()
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}
	proof := seal(serverToClientDigest(nonce, testPublicKey))
	err := h.VerifyServerBinding(Version4, proof)
	if !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("VerifyServerBinding version change error = %v, want ErrVersionChanged", err)
	}
	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrAborted) {
		t.Fatalf("SealCredential error = %v, want ErrAborted", err)
	}
}

// With a caller policy that permits version 2, the legacy binding must be
// exactly the public key bytes, and the server proof is the key with its
// first byte incremented. A tampered proof still aborts.
func TestLegacyV2BindingFlow(t *testing.T) {
	for _, version := range []int{Version2, Version3, Version4} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			h := newHandshake(t, &Config{
				Crypto:     mockCrypto{},
				MinVersion: Version2,
			})
			if err := h.Negotiate(version); err != nil {
				t.Fatalf("Negotiate(%d): %v", version, err)
			}
			if h.Version() != version {
				t.Fatalf("Version() = %d, want %d", h.Version(), version)
			}

			content, err := h.ClientBinding(testPublicKey)
			if err != nil {
				t.Fatalf("ClientBinding: %v", err)
			}
			contentPlain, err := mockCrypto{}.Decrypt(content)
			if err != nil {
				t.Fatalf("legacy binding not encrypted via callback: %v", err)
			}
			if !bytes.Equal(contentPlain, testPublicKey) {
				t.Fatalf("legacy binding = %x, want raw public key", contentPlain)
			}

			if err := h.VerifyServerBinding(version, seal(legacyProof(testPublicKey))); err != nil {
				t.Fatalf("VerifyServerBinding: %v", err)
			}
			if h.State() != StateVerified {
				t.Fatalf("state = %v, want StateVerified", h.State())
			}
			if _, err := h.SealCredential([]byte("secret")); err != nil {
				t.Fatalf("SealCredential: %v", err)
			}

			// Wrong first-byte-increment proof must be rejected.
			h2 := newHandshake(t, &Config{Crypto: mockCrypto{}, MinVersion: Version2})
			if err := h2.Negotiate(version); err != nil {
				t.Fatalf("second Negotiate: %v", err)
			}
			if _, err := h2.ClientBinding(testPublicKey); err != nil {
				t.Fatalf("second ClientBinding: %v", err)
			}
			bad := legacyProof(testPublicKey)
			bad[0]++
			if err := h2.VerifyServerBinding(version, seal(bad)); !errors.Is(err, ErrMismatch) {
				t.Fatalf("bad legacy proof error = %v, want ErrMismatch", err)
			}
		})
	}
}

func TestCredentialBeforeVerificationRejected(t *testing.T) {
	h := newHandshake(t, nil)

	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrState) {
		t.Fatalf("SealCredential before negotiation error = %v, want ErrState", err)
	}

	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrState) {
		t.Fatalf("SealCredential before verification error = %v, want ErrState", err)
	}

	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}
	if _, err := h.SealCredential([]byte("secret")); !errors.Is(err, ErrState) {
		t.Fatalf("SealCredential before server proof error = %v, want ErrState", err)
	}
}

func TestInvalidConfigAndCallOrder(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("New(nil) error = %v, want ErrConfig", err)
	}
	if _, err := New(&Config{}); !errors.Is(err, ErrConfig) {
		t.Fatalf("New without Crypto error = %v, want ErrConfig", err)
	}

	badConfigs := []*Config{
		{Crypto: mockCrypto{}, MinVersion: 1},
		{Crypto: mockCrypto{}, MaxVersion: 1},
		{Crypto: mockCrypto{}, MinVersion: Version5, MaxVersion: Version2},
	}
	for i, cfg := range badConfigs {
		if _, err := New(cfg); !errors.Is(err, ErrConfig) {
			t.Fatalf("bad config %d error = %v, want ErrConfig", i, err)
		}
	}

	h := newHandshake(t, nil)
	if _, err := h.ClientBinding(testPublicKey); !errors.Is(err, ErrState) {
		t.Fatalf("ClientBinding before Negotiate error = %v, want ErrState", err)
	}
	if err := h.CheckServerVersion(Version5); !errors.Is(err, ErrState) {
		t.Fatalf("CheckServerVersion before Negotiate error = %v, want ErrState", err)
	}

	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if err := h.Negotiate(Version5); !errors.Is(err, ErrState) {
		t.Fatalf("double Negotiate error = %v, want ErrState", err)
	}

	if _, err := h.ClientBinding(nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("ClientBinding(nil) error = %v, want ErrConfig", err)
	}
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted after empty key", h.State())
	}
}

func TestAbortWipesHandshake(t *testing.T) {
	h := newHandshake(t, nil)
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}

	h.Abort()
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted", h.State())
	}
	if !bytes.Equal(h.ClientNonce(), make([]byte, NonceSize)) {
		t.Fatal("nonce not wiped after Abort")
	}
	if err := h.Negotiate(Version5); !errors.Is(err, ErrAborted) {
		t.Fatalf("operation after Abort error = %v, want ErrAborted", err)
	}
}

func TestBindingLabelsIncludeNullByte(t *testing.T) {
	// Guard against the easy implementation mistake of omitting the trailing
	// 0x00 byte from either MS-CSSP label.
	for label, want := range map[string]string{
		clientToServerLabel: "CredSSP Client-To-Server Binding Hash",
		serverToClientLabel: "CredSSP Server-To-Client Binding Hash",
	} {
		if !strings.HasSuffix(label, "\x00") {
			t.Fatalf("label %q missing trailing zero byte", label)
		}
		if got := label[:len(label)-1]; got != want {
			t.Fatalf("label text = %q, want %q", got, want)
		}
	}
}

func TestEntropyAndEncryptFailuresAbort(t *testing.T) {
	if _, err := New(&Config{Crypto: mockCrypto{}, Rand: failingReader{}}); err == nil {
		t.Fatal("New with failing Rand: expected error, got nil")
	}

	h, err := New(&Config{Crypto: failingEncrypt{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h.ClientBinding(testPublicKey); err == nil {
		t.Fatal("ClientBinding with failing Encrypt: expected error, got nil")
	}
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted after encrypt failure", h.State())
	}

	// A credential-stage Encrypt failure must surface as an error without
	// producing a credential blob or tearing down an already-verified
	// handshake.
	crypto2 := &failOnSecondEncrypt{}
	h2, err := New(&Config{Crypto: crypto2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := h2.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if _, err := h2.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}
	proof := seal(serverToClientDigest(h2.ClientNonce(), testPublicKey))
	if err := h2.VerifyServerBinding(Version5, proof); err != nil {
		t.Fatalf("VerifyServerBinding: %v", err)
	}
	if _, err := h2.SealCredential([]byte("secret")); err == nil {
		t.Fatal("SealCredential with failing Encrypt: expected error, got nil")
	}
	if h2.State() != StateVerified {
		t.Fatalf("state = %v, want StateVerified preserved after credential encrypt failure",
			h2.State())
	}
}

func TestStableServerVersionAcceptedMidHandshake(t *testing.T) {
	h := newHandshake(t, nil)
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if err := h.CheckServerVersion(Version5); err != nil {
		t.Fatalf("CheckServerVersion with unchanged version: %v", err)
	}
	if h.State() != StateNegotiated {
		t.Fatalf("state = %v, want StateNegotiated", h.State())
	}
}

func TestProofOrderAndEmptyBlob(t *testing.T) {
	h := newHandshake(t, nil)
	if err := h.Negotiate(Version5); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	// Verification cannot run before the client binding is produced.
	if err := h.VerifyServerBinding(Version5, seal([]byte("x"))); !errors.Is(err, ErrState) {
		t.Fatalf("VerifyServerBinding before ClientBinding error = %v, want ErrState", err)
	}

	if _, err := h.ClientBinding(testPublicKey); err != nil {
		t.Fatalf("ClientBinding: %v", err)
	}

	// An empty server blob aborts instead of verifying.
	if err := h.VerifyServerBinding(Version5, nil); !errors.Is(err, ErrMismatch) {
		t.Fatalf("VerifyServerBinding(nil) error = %v, want ErrMismatch", err)
	}
	if h.State() != StateAborted {
		t.Fatalf("state = %v, want StateAborted", h.State())
	}

	// Every method on the aborted handshake keeps returning ErrAborted.
	if err := h.VerifyServerBinding(Version5, seal([]byte("x"))); !errors.Is(err, ErrAborted) {
		t.Fatalf("VerifyServerBinding after abort error = %v, want ErrAborted", err)
	}
	if err := h.CheckServerVersion(Version5); !errors.Is(err, ErrAborted) {
		t.Fatalf("CheckServerVersion after abort error = %v, want ErrAborted", err)
	}
}
