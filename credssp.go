package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
)

const (
	// ClientToServerBindingLabel prefixes the version 5+ pubKeyAuth hash.
	ClientToServerBindingLabel = "CredSSP Client-To-Server Binding Hash"
	// ServerToClientBindingLabel prefixes the version 5+ server checksum hash.
	ServerToClientBindingLabel = "CredSSP Server-To-Client Binding Hash"

	// ClientNonceSize is the size of the client nonce used by version 5+.
	ClientNonceSize = 32
	// BindingHashSize is the size of a SHA-256 binding hash.
	BindingHashSize = sha256.Size

	// MinSupportedVersion is the oldest CredSSP version understood here.
	MinSupportedVersion = 2
	// DefaultMaxVersion is the highest local CredSSP version by default.
	DefaultMaxVersion = 6
	// DefaultMinVersion rejects replay-prone legacy bindings by default.
	DefaultMinVersion = 5
)

var (
	// ErrInvalidVersion indicates a version outside the supported protocol.
	ErrInvalidVersion = errors.New("credssp: invalid protocol version")
	// ErrVersionDowngrade indicates that the negotiated version is below policy.
	ErrVersionDowngrade = errors.New("credssp: negotiated version below policy minimum")
	// ErrVersionChanged indicates that the server changed its version mid-handshake.
	ErrVersionChanged = errors.New("credssp: server version changed during handshake")
	// ErrInvalidNonce indicates that a client nonce is not exactly 32 bytes.
	ErrInvalidNonce = errors.New("credssp: client nonce must be 32 bytes")
	// ErrNonceReuse indicates that the nonce source repeated an earlier value.
	ErrNonceReuse = errors.New("credssp: client nonce reused")
	// ErrMissingPublicKey indicates that no server public key was supplied.
	ErrMissingPublicKey = errors.New("credssp: server public key is required")
	// ErrInvalidHash indicates that a hash callback did not return 32 bytes.
	ErrInvalidHash = errors.New("credssp: binding hash must be 32 bytes")
	// ErrChecksumMismatch indicates that the server binding check failed.
	ErrChecksumMismatch = errors.New("credssp: server binding checksum mismatch")
	// ErrNoActiveHandshake indicates that Complete was called out of order.
	ErrNoActiveHandshake = errors.New("credssp: no active handshake")
	// ErrHandshakeActive indicates that Begin was called twice without Complete.
	ErrHandshakeActive = errors.New("credssp: handshake already active")
)

// HashFunc is the hash callback used for version 5+ bindings. It is expected
// to implement SHA-256 and return exactly BindingHashSize bytes.
type HashFunc func([]byte) []byte

// CredentialsSender sends credentials after the server binding has been
// verified. Encryption and transport are the callback's responsibility.
type CredentialsSender func(context.Context) error

// Config controls a CredSSP client binding handshake.
type Config struct {
	// MaxVersion is the highest protocol version supported locally. Zero
	// selects DefaultMaxVersion.
	MaxVersion int
	// MinVersion is the lowest acceptable negotiated version. Zero selects
	// DefaultMinVersion. Set it explicitly to allow legacy versions 2 through 4.
	MinVersion int
	// Hash implements SHA-256. A nil callback uses crypto/sha256.
	Hash HashFunc
	// Random supplies nonce bytes. A nil reader uses crypto/rand.Reader.
	Random io.Reader
	// SendCredentials is called only after a successful server binding check.
	// It may be nil for callers that only need the binding primitives.
	SendCredentials CredentialsSender
}

// ClientBinding is the client-to-server portion of a CredSSP handshake.
type ClientBinding struct {
	// Version is min(Config.MaxVersion, serverVersion).
	Version int
	// ClientNonce is a fresh 32-byte nonce for this handshake.
	ClientNonce []byte
	// PubKeyAuth is the value bound to the server certificate public key.
	PubKeyAuth []byte
}

// ServerReply is the server-to-client binding response.
type ServerReply struct {
	// Version must be identical to the version supplied to Begin.
	Version int
	// Checksum is the server-to-client binding value.
	Checksum []byte
}

type handshakeState uint8

const (
	handshakeIdle handshakeState = iota
	handshakeActive
	handshakeComplete
)

// Client performs a CredSSP public-key binding handshake. A Client is not safe
// for concurrent use. It may be reused for sequential handshakes; every Begin
// generates a new client nonce.
type Client struct {
	cfg Config

	state         handshakeState
	serverVersion int
	version       int
	nonce         []byte
	publicKey     []byte
	lastNonce     []byte
}

// NewClient validates config and returns a CredSSP binding client. By default
// legacy versions 2 through 4 are rejected because their binding is replayable.
func NewClient(cfg Config) (*Client, error) {
	if cfg.MaxVersion == 0 {
		cfg.MaxVersion = DefaultMaxVersion
	}
	if cfg.MinVersion == 0 {
		cfg.MinVersion = DefaultMinVersion
	}
	if cfg.MinVersion < MinSupportedVersion {
		return nil, fmt.Errorf("%w: minimum %d is below %d", ErrInvalidVersion, cfg.MinVersion, MinSupportedVersion)
	}
	if cfg.MaxVersion < cfg.MinVersion {
		return nil, fmt.Errorf("%w: maximum %d is below minimum %d", ErrInvalidVersion, cfg.MaxVersion, cfg.MinVersion)
	}
	if cfg.Hash == nil {
		cfg.Hash = sha256Hash
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	return &Client{cfg: cfg}, nil
}

// NegotiateVersion returns the smaller of the local maximum and the version
// reported by the server, as required by CredSSP version negotiation.
func NegotiateVersion(localMaximum, serverVersion int) int {
	if localMaximum < serverVersion {
		return localMaximum
	}
	return serverVersion
}

// Begin negotiates the protocol version, creates a fresh client nonce, and
// computes the client's pubKeyAuth value. It aborts without generating a nonce
// if the negotiated version is below the caller's policy.
func (c *Client) Begin(serverVersion int, serverPublicKey []byte) (ClientBinding, error) {
	if c.state == handshakeActive {
		return ClientBinding{}, ErrHandshakeActive
	}
	if len(serverPublicKey) == 0 {
		return ClientBinding{}, ErrMissingPublicKey
	}

	version := NegotiateVersion(c.cfg.MaxVersion, serverVersion)
	if version < c.cfg.MinVersion || version < MinSupportedVersion {
		return ClientBinding{}, fmt.Errorf(
			"%w: server=%d local-max=%d policy-min=%d",
			ErrVersionDowngrade, serverVersion, c.cfg.MaxVersion, c.cfg.MinVersion,
		)
	}

	nonce := make([]byte, ClientNonceSize)
	if _, err := io.ReadFull(c.cfg.Random, nonce); err != nil {
		return ClientBinding{}, fmt.Errorf("credssp: generate client nonce: %w", err)
	}
	if len(c.lastNonce) == ClientNonceSize && subtle.ConstantTimeCompare(c.lastNonce, nonce) == 1 {
		return ClientBinding{}, ErrNonceReuse
	}
	c.lastNonce = append(c.lastNonce[:0], nonce...)

	pubKeyAuth, err := ComputePubKeyAuth(version, serverPublicKey, nonce, c.cfg.Hash)
	if err != nil {
		return ClientBinding{}, err
	}

	c.state = handshakeActive
	c.serverVersion = serverVersion
	c.version = version
	c.nonce = append(c.nonce[:0], nonce...)
	c.publicKey = append(c.publicKey[:0], serverPublicKey...)

	return ClientBinding{
		Version:     version,
		ClientNonce: append([]byte(nil), nonce...),
		PubKeyAuth:  pubKeyAuth,
	}, nil
}

// Complete validates the server's version and binding checksum. Credentials are
// sent only after an exact constant-time checksum match. Any failure closes the
// active handshake and leaves the credential callback untouched.
func (c *Client) Complete(ctx context.Context, reply ServerReply) error {
	if c.state != handshakeActive {
		return ErrNoActiveHandshake
	}

	if reply.Version != c.serverVersion {
		c.state = handshakeIdle
		return fmt.Errorf(
			"%w: initial=%d reply=%d",
			ErrVersionChanged, c.serverVersion, reply.Version,
		)
	}

	expected, err := ComputeServerChecksum(c.version, c.publicKey, c.nonce, c.cfg.Hash)
	if err != nil {
		c.state = handshakeIdle
		return err
	}
	if subtle.ConstantTimeCompare(expected, reply.Checksum) != 1 {
		c.state = handshakeIdle
		return ErrChecksumMismatch
	}

	c.state = handshakeComplete
	if c.cfg.SendCredentials == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.cfg.SendCredentials(ctx); err != nil {
		return fmt.Errorf("credssp: send credentials: %w", err)
	}
	return nil
}

// ComputePubKeyAuth computes the client-to-server public-key binding.
//
// Versions 2 through 4 return the server public key itself. Version 5 and
// later return SHA-256(label || 0x00 || clientNonce || publicKey).
func ComputePubKeyAuth(version int, publicKey, clientNonce []byte, hash HashFunc) ([]byte, error) {
	if len(publicKey) == 0 {
		return nil, ErrMissingPublicKey
	}
	switch {
	case version >= 5:
		return computeBindingHash(hash, ClientToServerBindingLabel, clientNonce, publicKey)
	case version >= MinSupportedVersion:
		return append([]byte(nil), publicKey...), nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrInvalidVersion, version)
	}
}

// ComputeServerChecksum computes the value expected in the server reply.
//
// Versions 2 through 4 use the public key with its first byte incremented.
// Version 5 and later use SHA-256(label || 0x00 || clientNonce || publicKey).
func ComputeServerChecksum(version int, publicKey, clientNonce []byte, hash HashFunc) ([]byte, error) {
	if len(publicKey) == 0 {
		return nil, ErrMissingPublicKey
	}
	switch {
	case version >= 5:
		return computeBindingHash(hash, ServerToClientBindingLabel, clientNonce, publicKey)
	case version >= MinSupportedVersion:
		checksum := append([]byte(nil), publicKey...)
		checksum[0]++
		return checksum, nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrInvalidVersion, version)
	}
}

// VerifyServerChecksum compares the server binding value in constant time.
func VerifyServerChecksum(version int, publicKey, clientNonce, checksum []byte, hash HashFunc) error {
	expected, err := ComputeServerChecksum(version, publicKey, clientNonce, hash)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(expected, checksum) != 1 {
		return ErrChecksumMismatch
	}
	return nil
}

func computeBindingHash(hash HashFunc, label string, clientNonce, publicKey []byte) ([]byte, error) {
	if len(clientNonce) != ClientNonceSize {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidNonce, len(clientNonce))
	}
	if hash == nil {
		hash = sha256Hash
	}

	input := make([]byte, 0, len(label)+1+len(clientNonce)+len(publicKey))
	input = append(input, label...)
	input = append(input, 0)
	input = append(input, clientNonce...)
	input = append(input, publicKey...)

	digest := hash(input)
	if len(digest) != BindingHashSize {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidHash, len(digest))
	}
	return append([]byte(nil), digest...), nil
}

func sha256Hash(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
