package netsec

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"time"
)

const rootKeyInfoDomain = "example.org/netsec/q0154/root-key/v1"

// PreKeyMode describes how much one-time key material a session obtained.
type PreKeyMode uint8

const (
	// PreKeyModeOneTimePreKey means a fresh OPK contributed to the root key.
	PreKeyModeOneTimePreKey PreKeyMode = iota + 1
	// PreKeyModeSignedPreKeyOnly means the session has no OPK contribution.
	PreKeyModeSignedPreKeyOnly
)

func (m PreKeyMode) String() string {
	switch m {
	case PreKeyModeOneTimePreKey:
		return "one-time-prekey"
	case PreKeyModeSignedPreKeyOnly:
		return "signed-prekey-only"
	default:
		return "unknown"
	}
}

// DirectoryProofCallback is the host's directory lookup/attestation hook. It
// must confirm that bundle belongs to target. Verification that a bundle is
// published says nothing about whether the target device is currently online.
type DirectoryProofCallback func(ctx context.Context, target PublicIdentity, bundle *PreKeyBundle) ([]byte, error)

// DirectoryProofVerifier optionally lets a receiver check a proof carried by a
// first message. Proof contents are defined by the messaging host's directory.
type DirectoryProofVerifier func(ctx context.Context, target PublicIdentity, proof []byte) error

// InitiatorConfig configures an asynchronous handshake initiator.
type InitiatorConfig struct {
	Identity       *DeviceIdentity
	DirectoryProof DirectoryProofCallback
	Now            func() time.Time
}

// Initiator starts sessions from receiver prekey bundles.
type Initiator struct {
	identity  *DeviceIdentity
	directory DirectoryProofCallback
	now       func() time.Time
}

// NewInitiator validates and creates an initiator.
func NewInitiator(cfg InitiatorConfig) (*Initiator, error) {
	if cfg.Identity == nil {
		return nil, fmt.Errorf("%w: missing initiator identity", ErrInvalidIdentity)
	}
	if err := cfg.Identity.Public().Validate(); err != nil {
		return nil, err
	}
	if cfg.DirectoryProof == nil {
		return nil, fmt.Errorf("%w: missing directory proof callback", ErrDirectoryProof)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Initiator{identity: cfg.Identity, directory: cfg.DirectoryProof, now: now}, nil
}

// InitiatorHandshake is an established initiator-side session and its first message.
type InitiatorHandshake struct {
	message        *InitialMessage
	transcript     []byte
	transcriptHash [32]byte
	rootKey        [32]byte
	mode           PreKeyMode
}

// Start creates a session from a directory bundle and encrypts the first
// plaintext. The returned handshake can be retransmitted byte-for-byte.
func (i *Initiator) Start(target PublicIdentity, bundle *PreKeyBundle, firstPlaintext []byte) (*InitiatorHandshake, error) {
	return i.StartContext(context.Background(), target, bundle, firstPlaintext)
}

// StartContext is Start with a context for the directory proof callback.
func (i *Initiator) StartContext(ctx context.Context, target PublicIdentity, bundle *PreKeyBundle, firstPlaintext []byte) (*InitiatorHandshake, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(firstPlaintext) > maxFirstPlaintextSize {
		return nil, fmt.Errorf("%w: first plaintext is too large", ErrInvalidMessage)
	}
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if err := bundle.Verify(i.now()); err != nil {
		return nil, err
	}
	bundleIdentity, err := bundle.RegisteredIdentity()
	if err != nil {
		return nil, err
	}
	if !bundleIdentity.Equal(target) {
		return nil, fmt.Errorf("%w: bundle is for %s/%s", ErrIdentityMismatch, bundle.AccountID, bundle.DeviceID)
	}
	proof, err := i.directory(ctx, target, bundle)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDirectoryProof, err)
	}
	if len(proof) == 0 {
		return nil, fmt.Errorf("%w: empty proof", ErrDirectoryProof)
	}

	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate ephemeral key: %w", err)
	}

	dh1, err := ecdhSecret(ephemeral, bundle.IdentityKey)
	if err != nil {
		return nil, err
	}
	dh2, err := ecdhSecret(ephemeral, bundle.SignedPreKey)
	if err != nil {
		return nil, err
	}
	dh3, err := ecdhSecret(i.identity.identityPrivate, bundle.SignedPreKey)
	if err != nil {
		return nil, err
	}
	secrets := [][]byte{dh1, dh2, dh3}
	mode := PreKeyModeSignedPreKeyOnly
	if bundle.OneTimePreKey != nil {
		dh4, err := ecdhSecret(ephemeral, bundle.OneTimePreKey)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, dh4)
		mode = PreKeyModeOneTimePreKey
	}

	transcript := Transcript{
		Version:          ProtocolVersion,
		Initiator:        i.identity.Public(),
		Receiver:         target.clone(),
		EphemeralKey:     ephemeral.PublicKey().Bytes(),
		SignedPreKeyID:   bundle.SignedPreKeyID,
		SignedPreKey:     bundle.SignedPreKey.Bytes(),
		HasOneTimePreKey: bundle.OneTimePreKey != nil,
		OneTimePreKeyID:  bundle.OneTimePreKeyID,
		BundleExpiresAt:  bundle.ExpiresAt,
	}
	if bundle.OneTimePreKey != nil {
		transcript.OneTimePreKey = bundle.OneTimePreKey.Bytes()
	}
	transcript.DirectoryProofDigest = sha256.Sum256(proof)
	transcriptBytes := transcript.encode()
	transcriptHash := sha256.Sum256(transcriptBytes)

	rootKey, err := deriveRootKey(secrets, transcriptHash)
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(i.identity.signingPrivate, initiatorSignatureInput(transcriptBytes))
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("netsec: generate AEAD nonce: %w", err)
	}
	ciphertext, err := sealAEAD(rootKey[:], nonce, firstPlaintext, transcriptBytes)
	if err != nil {
		return nil, err
	}
	message := &InitialMessage{
		Version:            ProtocolVersion,
		Sender:             i.identity.Public(),
		RecipientAccountID: target.AccountID,
		RecipientDeviceID:  target.DeviceID,
		Transcript:         transcriptBytes,
		DirectoryProof:     append([]byte(nil), proof...),
		Nonce:              nonce,
		Ciphertext:         ciphertext,
		Signature:          signature,
	}
	return &InitiatorHandshake{
		message:        message,
		transcript:     append([]byte(nil), transcriptBytes...),
		transcriptHash: transcriptHash,
		rootKey:        rootKey,
		mode:           mode,
	}, nil
}

// Message returns a defensive copy of the first message.
func (h *InitiatorHandshake) Message() *InitialMessage {
	if h == nil {
		return nil
	}
	return h.message.Clone()
}

// MessageBytes returns the canonical bytes to retransmit after a lost response.
func (h *InitiatorHandshake) MessageBytes() ([]byte, error) {
	if h == nil || h.message == nil {
		return nil, fmt.Errorf("%w: nil handshake", ErrInvalidMessage)
	}
	return h.message.MarshalBinary()
}

// RootKey returns a copy of the 32-byte root key.
func (h *InitiatorHandshake) RootKey() []byte {
	if h == nil {
		return nil
	}
	return append([]byte(nil), h.rootKey[:]...)
}

// Transcript returns a copy of the bound transcript bytes.
func (h *InitiatorHandshake) Transcript() []byte {
	if h == nil {
		return nil
	}
	return append([]byte(nil), h.transcript...)
}

// TranscriptDigest returns the transcript hash used as the HKDF salt.
func (h *InitiatorHandshake) TranscriptDigest() [32]byte {
	if h == nil {
		return [32]byte{}
	}
	return h.transcriptHash
}

// Mode reports whether an OPK contributed to the session.
func (h *InitiatorHandshake) Mode() PreKeyMode {
	if h == nil {
		return 0
	}
	return h.mode
}

func ecdhSecret(privateKey *ecdh.PrivateKey, publicKey *ecdh.PublicKey) ([]byte, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("%w: missing private key", ErrInvalidPoint)
	}
	if len(privateKey.PublicKey().Bytes()) != 65 {
		return nil, fmt.Errorf("%w: private key is not P-256", ErrInvalidPoint)
	}
	if err := validateP256Public("ECDH public key", publicKey); err != nil {
		return nil, err
	}
	secret, err := privateKey.ECDH(publicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: ECDH: %v", ErrInvalidPoint, err)
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("%w: ECDH secret has %d bytes", ErrInvalidPoint, len(secret))
	}
	if allZero(secret) {
		return nil, fmt.Errorf("%w: all-zero ECDH result", ErrInvalidPoint)
	}
	return secret, nil
}

func deriveRootKey(secrets [][]byte, transcriptDigest [32]byte) ([32]byte, error) {
	if len(secrets) < 3 {
		return [32]byte{}, fmt.Errorf("%w: too few DH contributions", ErrInvalidPoint)
	}
	ikm := make([]byte, 0, 32*len(secrets))
	for _, secret := range secrets {
		if len(secret) != 32 || allZero(secret) {
			return [32]byte{}, fmt.Errorf("%w: bad DH contribution", ErrInvalidPoint)
		}
		ikm = append(ikm, secret...)
	}
	key, err := hkdf.Key(sha256.New, ikm, transcriptDigest[:], rootKeyInfoDomain, 32)
	if err != nil {
		return [32]byte{}, fmt.Errorf("netsec: HKDF: %w", err)
	}
	var rootKey [32]byte
	copy(rootKey[:], key)
	return rootKey, nil
}

func sealAEAD(key, nonce, plaintext, associatedData []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("netsec: AEAD cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: AEAD mode: %w", err)
	}
	return gcm.Seal(nil, nonce, plaintext, associatedData), nil
}

func openAEAD(key, nonce, ciphertext, associatedData []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	return plaintext, nil
}

func allZero(value []byte) bool {
	var combined byte
	for _, b := range value {
		combined |= b
	}
	return combined == 0
}
