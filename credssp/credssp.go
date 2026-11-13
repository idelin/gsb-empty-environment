// Package credssp implements the CredSSP client-side public key binding used
// by Remote Desktop credential delegation, with explicit version-negotiation
// policy to prevent protocol downgrade.
//
// The package deliberately performs no transport, ASN.1 marshalling, key
// exchange or cryptography of its own. Encryption, decryption and hashing are
// supplied by the caller through callbacks, which makes the binding logic
// independent of any particular TLS or NTLM/Kerberos backend. See [Crypto].
//
// References: [MS-CSSP] Credential Security Support Provider protocol,
// in particular the TSRequest version field and the version 5 public key
// binding hashes.
//
// [MS-CSSP]: https://learn.microsoft.com/openspecs/windows_protocols/ms-cssp
package credssp

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
)

// CredSSP protocol versions. Versions below 2 are never accepted by this
// package; version 5 is the first version whose public key binding includes
// the client nonce.
const (
	Version2 = 2
	Version3 = 3
	Version4 = 4
	Version5 = 5
)

// DefaultMinVersion is the minimum negotiated version used when
// [Config.MinVersion] is left at zero. A downgraded handshake below version 5
// cannot bind a client nonce and is therefore rejected by default.
const DefaultMinVersion = Version5

// NonceSize is the size, in bytes, of the client nonce generated fresh for
// every handshake.
const NonceSize = 32

const (
	clientToServerLabel = "CredSSP Client-To-Server Binding Hash\x00"
	serverToClientLabel = "CredSSP Server-To-Client Binding Hash\x00"
)

var (
	// ErrConfig is returned when the handshake configuration is invalid.
	ErrConfig = errors.New("credssp: invalid configuration")
	// ErrState is returned when a handshake method is called out of order or
	// on a handshake that has already finished.
	ErrState = errors.New("credssp: handshake in invalid state for this operation")
	// ErrAborted is returned for every operation on an aborted handshake.
	ErrAborted = errors.New("credssp: handshake aborted")
	// ErrUnsupportedVersion is returned when a peer advertises a version this
	// package does not speak.
	ErrUnsupportedVersion = errors.New("credssp: unsupported protocol version")
	// ErrDowngrade is returned when the negotiated version is below the
	// caller's configured minimum.
	ErrDowngrade = errors.New("credssp: protocol version downgraded below configured minimum")
	// ErrVersionChanged is returned when the version in a server TSRequest
	// differs from the version the server advertised at negotiation time.
	ErrVersionChanged = errors.New("credssp: server changed protocol version mid-handshake")
	// ErrMismatch is returned when the server's public key proof does not
	// match the value expected for the negotiated version and public key.
	ErrMismatch = errors.New("credssp: server public key proof verification failed")
)

// Crypto supplies the cryptographic operations needed by a handshake.
//
// Encrypt seals the plaintext pubKeyAuth or credential payload with the
// established CredSSP session key and returns the TSRequest authInfo/pubKeyAuth
// blob. Decrypt performs the inverse operation. Hash must compute the
// version 5 binding digest; implementations should normally use SHA-256.
type Crypto interface {
	Encrypt(plaintext []byte) (blob []byte, err error)
	Decrypt(blob []byte) (plaintext []byte, err error)
	Hash(data []byte) []byte
}

// Config configures a client handshake.
type Config struct {
	// MaxVersion is the highest version the client offers. Zero defaults to
	// [Version5].
	MaxVersion int
	// MinVersion is the lowest version the caller's policy accepts. Zero
	// defaults to [DefaultMinVersion] (5). A negotiated version below this
	// aborts the handshake with [ErrDowngrade].
	MinVersion int
	// Crypto provides encryption, decryption and hashing callbacks and is
	// required.
	Crypto Crypto
	// Rand is used to generate the 32-byte client nonce. If nil,
	// [crypto/rand.Reader] is used. Tests may inject a deterministic reader.
	Rand io.Reader
}

// State reports the point a [Handshake] has reached.
type State int

const (
	// StateNew is the state after [New], before version negotiation.
	StateNew State = iota
	// StateNegotiated means the version has been negotiated and accepted.
	StateNegotiated
	// StateSentClientBinding means the client pubKeyAuth content was produced.
	StateSentClientBinding
	// StateVerified means the server proof was verified and credentials may
	// be sealed.
	StateVerified
	// StateAborted means the handshake failed; every later operation fails.
	StateAborted
)

// Handshake drives one client-side CredSSP public key binding exchange. A
// fresh Handshake must be created for every connection so that a new nonce is
// generated.
type Handshake struct {
	maxVersion      int
	minVersion      int
	crypto          Crypto
	state           State
	serverVersion   int
	negotiated      int
	nonce           []byte
	serverPublicKey []byte
}

// New validates the configuration, generates a fresh 32-byte client nonce and
// returns a handshake in [StateNew].
func New(cfg *Config) (*Handshake, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: nil config", ErrConfig)
	}
	if cfg.Crypto == nil {
		return nil, fmt.Errorf("%w: missing Crypto callbacks", ErrConfig)
	}

	minVersion := cfg.MinVersion
	if minVersion == 0 {
		minVersion = DefaultMinVersion
	}
	maxVersion := cfg.MaxVersion
	if maxVersion == 0 {
		maxVersion = Version5
	}
	if minVersion < Version2 || maxVersion < Version2 {
		return nil, fmt.Errorf("%w: versions below %d are not supported", ErrConfig, Version2)
	}
	if maxVersion < minVersion {
		return nil, fmt.Errorf("%w: MaxVersion (%d) below MinVersion (%d)",
			ErrConfig, maxVersion, minVersion)
	}

	random := cfg.Rand
	if random == nil {
		random = rand.Reader
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, fmt.Errorf("credssp: generate client nonce: %w", err)
	}

	return &Handshake{
		maxVersion: maxVersion,
		minVersion: minVersion,
		crypto:     cfg.Crypto,
		state:      StateNew,
		nonce:      nonce,
	}, nil
}

// State returns the current handshake state.
func (h *Handshake) State() State {
	return h.state
}

// Version returns the negotiated protocol version, or zero before negotiation.
func (h *Handshake) Version() int {
	if h.state == StateNew || h.state == StateAborted {
		return 0
	}
	return h.negotiated
}

// ClientNonce returns a copy of the 32-byte client nonce generated for this
// handshake.
func (h *Handshake) ClientNonce() []byte {
	out := make([]byte, len(h.nonce))
	copy(out, h.nonce)
	return out
}

// Negotiate consumes the version field from the server's first TSRequest and
// applies the downgrade policy:
//
//   - the negotiated version is min(client max version, server version);
//   - a negotiated version below Config.MinVersion (default 5) aborts with
//     [ErrDowngrade];
//   - a server version outside the supported range (2..5) aborts with
//     [ErrUnsupportedVersion].
//
// The raw server version is remembered so that [CheckServerVersion] can
// detect mid-handshake changes.
func (h *Handshake) Negotiate(serverVersion int) error {
	if h.state == StateAborted {
		return ErrAborted
	}
	if h.state != StateNew {
		return fmt.Errorf("%w: negotiation already done", ErrState)
	}
	if serverVersion < Version2 || serverVersion > Version5 {
		return h.abort(ErrUnsupportedVersion)
	}

	h.serverVersion = serverVersion

	negotiated := h.maxVersion
	if serverVersion < negotiated {
		negotiated = serverVersion
	}
	if negotiated < h.minVersion {
		return h.abort(fmt.Errorf("%w: negotiated version %d, minimum %d",
			ErrDowngrade, negotiated, h.minVersion))
	}

	h.negotiated = negotiated
	h.state = StateNegotiated
	return nil
}

// CheckServerVersion compares the version field of a later server TSRequest
// with the version the server advertised in its first reply. Any change
// aborts the handshake with [ErrVersionChanged].
func (h *Handshake) CheckServerVersion(serverVersion int) error {
	if h.state == StateAborted {
		return ErrAborted
	}
	if h.state == StateNew {
		return fmt.Errorf("%w: Negotiate must be called first", ErrState)
	}
	if serverVersion != h.serverVersion {
		return h.abort(fmt.Errorf("%w: negotiated with %d, server now reports %d",
			ErrVersionChanged, h.serverVersion, serverVersion))
	}
	return nil
}

// ClientBinding builds the content for the client pubKeyAuth TSRequest field
// and returns it sealed through [Crypto.Encrypt], ready to place in the
// TSRequest pubKeyAuth field.
//
// For negotiated versions 2 through 4 the content is the DER-encoded public
// key bytes from the server certificate. For version 5 and later it is
//
//	Hash("CredSSP Client-To-Server Binding Hash" || 0x00 ||
//	     clientNonce || serverPublicKey)
//
// where Hash comes from [Crypto] (SHA-256 in the standard protocol). This
// method never transmits anything; the caller owns the TSRequest transport.
func (h *Handshake) ClientBinding(serverPublicKey []byte) ([]byte, error) {
	if h.state == StateAborted {
		return nil, ErrAborted
	}
	if h.state != StateNegotiated {
		return nil, fmt.Errorf("%w: ClientBinding requires negotiated state", ErrState)
	}
	if len(serverPublicKey) == 0 {
		return nil, h.abort(fmt.Errorf("%w: empty server public key", ErrConfig))
	}

	h.serverPublicKey = append([]byte(nil), serverPublicKey...)

	var content []byte
	if h.negotiated < Version5 {
		content = append([]byte(nil), h.serverPublicKey...)
	} else {
		content = h.clientBindingHash()
	}
	blob, err := h.crypto.Encrypt(content)
	if err != nil {
		return nil, h.abort(fmt.Errorf("credssp: encrypt client binding: %w", err))
	}
	h.state = StateSentClientBinding
	return blob, nil
}

// VerifyServerBinding validates the encrypted pubKeyAuth blob returned by the
// server. It first enforces that the server TSRequest still carries the
// originally advertised version, decrypts the blob through
// [Crypto.Decrypt], and compares the plaintext against the expected proof in
// constant time:
//
//   - version 5 and later:
//     Hash("CredSSP Server-To-Client Binding Hash" || 0x00 ||
//     clientNonce || serverPublicKey);
//   - versions 2 through 4: the public key with its first byte incremented by
//     one and every remaining byte unchanged.
//
// Any mismatch, decrypt failure or version change aborts the handshake.
// [SealCredential] refuses to run until this method succeeds, so a failed
// proof never results in credentials being sent.
func (h *Handshake) VerifyServerBinding(serverVersion int, encryptedProof []byte) error {
	if h.state == StateAborted {
		return ErrAborted
	}
	if h.state != StateSentClientBinding {
		return fmt.Errorf("%w: VerifyServerBinding requires a sent client binding", ErrState)
	}
	if err := h.CheckServerVersion(serverVersion); err != nil {
		return err
	}
	if len(encryptedProof) == 0 {
		return h.abort(fmt.Errorf("%w: empty server proof blob", ErrMismatch))
	}

	proof, err := h.crypto.Decrypt(append([]byte(nil), encryptedProof...))
	if err != nil {
		return h.abort(fmt.Errorf("%w: decrypt server proof: %v", ErrMismatch, err))
	}

	expected := h.expectedServerProof()
	if subtle.ConstantTimeCompare(proof, expected) != 1 {
		return h.abort(fmt.Errorf("%w: proof does not match version %d binding",
			ErrMismatch, h.negotiated))
	}

	h.state = StateVerified
	return nil
}

// SealCredential encrypts the caller's credential payload through
// [Crypto.Encrypt]. It is only permitted after [VerifyServerBinding] has
// succeeded; any aborted or not-yet-verified handshake returns an error and
// produces no blob.
func (h *Handshake) SealCredential(credential []byte) ([]byte, error) {
	if h.state == StateAborted {
		return nil, ErrAborted
	}
	if h.state != StateVerified {
		return nil, fmt.Errorf("%w: credentials may only be sealed after successful server verification",
			ErrState)
	}
	blob, err := h.crypto.Encrypt(append([]byte(nil), credential...))
	if err != nil {
		return nil, fmt.Errorf("credssp: encrypt credential: %w", err)
	}
	return blob, nil
}

// Abort terminates the handshake and wipes its nonce and stored public key.
// Subsequent operations return [ErrAborted].
func (h *Handshake) Abort() {
	h.abort(nil)
}

func (h *Handshake) abort(err error) error {
	for i := range h.nonce {
		h.nonce[i] = 0
	}
	for i := range h.serverPublicKey {
		h.serverPublicKey[i] = 0
	}
	h.serverPublicKey = nil
	h.state = StateAborted
	if err == nil {
		return ErrAborted
	}
	return err
}

func (h *Handshake) clientBindingHash() []byte {
	buf := make([]byte, 0, len(clientToServerLabel)+NonceSize+len(h.serverPublicKey))
	buf = append(buf, clientToServerLabel...)
	buf = append(buf, h.nonce...)
	buf = append(buf, h.serverPublicKey...)
	return h.crypto.Hash(buf)
}

func (h *Handshake) serverBindingHash() []byte {
	buf := make([]byte, 0, len(serverToClientLabel)+NonceSize+len(h.serverPublicKey))
	buf = append(buf, serverToClientLabel...)
	buf = append(buf, h.nonce...)
	buf = append(buf, h.serverPublicKey...)
	return h.crypto.Hash(buf)
}

func (h *Handshake) expectedServerProof() []byte {
	if h.negotiated >= Version5 {
		return h.serverBindingHash()
	}

	// Versions 2-4: increment the first byte of the public key and keep all
	// remaining bytes identical.
	proof := append([]byte(nil), h.serverPublicKey...)
	proof[0]++
	return proof
}
