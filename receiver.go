package netsec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"sync"
	"time"
)

// ReceiverConfig configures a device that publishes prekey bundles.
type ReceiverConfig struct {
	Identity               *DeviceIdentity
	SignedPreKey           *SignedPreKey
	OneTimePreKeys         []*OneTimePreKey
	ExpiresAt              time.Time
	DirectoryProofVerifier DirectoryProofVerifier
	Now                    func() time.Time
}

// Receiver owns receiver private prekeys and the idempotency state for first
// messages. A single Receiver must be used for the lifetime of a published OPK
// so that its atomic consumption marker cannot be bypassed.
type Receiver struct {
	identity       *DeviceIdentity
	signedPreKey   *SignedPreKey
	oneTimePreKeys map[uint32]*OneTimePreKey
	expiresAt      time.Time
	now            func() time.Time
	verifyProof    DirectoryProofVerifier

	mu       sync.Mutex
	usedOPKs map[uint32]bool
	sessions map[[32]byte]*receiverSession
}

type receiverSession struct {
	messageDigest [32]byte
	result        *ReceiveResult
}

// ReceiveResult is the receiver-side session that exists after the first
// authenticated plaintext has been accepted.
type ReceiveResult struct {
	Plaintext         []byte
	RootKey           []byte
	Transcript        []byte
	TranscriptHash    [32]byte
	OneTimePreKeyID   uint32
	OneTimePreKeyUsed bool
	Mode              PreKeyMode
	Replayed          bool
}

func (r *ReceiveResult) clone() *ReceiveResult {
	if r == nil {
		return nil
	}
	c := *r
	c.Plaintext = append([]byte(nil), r.Plaintext...)
	c.RootKey = append([]byte(nil), r.RootKey...)
	c.Transcript = append([]byte(nil), r.Transcript...)
	return &c
}

// NewReceiver validates receiver keys and creates receive state.
func NewReceiver(cfg ReceiverConfig) (*Receiver, error) {
	if cfg.Identity == nil {
		return nil, fmt.Errorf("%w: missing receiver identity", ErrInvalidIdentity)
	}
	if err := cfg.Identity.Public().Validate(); err != nil {
		return nil, err
	}
	if cfg.SignedPreKey == nil {
		return nil, fmt.Errorf("%w: missing signed prekey", ErrInvalidBundle)
	}
	signedPreKey, err := NewSignedPreKeyFromPrivate(cfg.SignedPreKey.ID, cfg.SignedPreKey.PrivateKey)
	if err != nil {
		return nil, err
	}
	if cfg.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("%w: missing bundle expiry", ErrInvalidBundle)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	oneTimePreKeys := make(map[uint32]*OneTimePreKey, len(cfg.OneTimePreKeys))
	for _, opk := range cfg.OneTimePreKeys {
		if opk == nil {
			return nil, fmt.Errorf("%w: nil one-time prekey", ErrInvalidBundle)
		}
		if _, exists := oneTimePreKeys[opk.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate one-time prekey ID %d", ErrInvalidBundle, opk.ID)
		}
		canonical, err := NewOneTimePreKeyFromPrivate(opk.ID, opk.PrivateKey)
		if err != nil {
			return nil, err
		}
		oneTimePreKeys[opk.ID] = canonical
	}
	return &Receiver{
		identity:       cfg.Identity,
		signedPreKey:   signedPreKey,
		oneTimePreKeys: oneTimePreKeys,
		expiresAt:      cfg.ExpiresAt,
		now:            now,
		verifyProof:    cfg.DirectoryProofVerifier,
		usedOPKs:       make(map[uint32]bool),
		sessions:       make(map[[32]byte]*receiverSession),
	}, nil
}

// Public returns this receiver's registered public identity.
func (r *Receiver) Public() PublicIdentity {
	return r.identity.Public()
}

// PreKeyBundle publishes a signed bundle. An opkID of zero means that the
// bundle deliberately has no one-time prekey.
func (r *Receiver) PreKeyBundle(opkID uint32) (*PreKeyBundle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var opk *OneTimePreKey
	if opkID != 0 {
		if r.usedOPKs[opkID] {
			return nil, fmt.Errorf("%w: %d", ErrOneTimePreKeyUsed, opkID)
		}
		opk = r.oneTimePreKeys[opkID]
		if opk == nil {
			return nil, fmt.Errorf("%w: %d", ErrUnknownOneTimePreKey, opkID)
		}
	}
	return BuildPreKeyBundle(r.identity, r.signedPreKey, opk, r.expiresAt)
}

// AcceptInitial authenticates and decrypts the first message. The OPK's
// check-and-consume and the cached result are committed under one lock, before
// any plaintext is returned to the caller.
func (r *Receiver) AcceptInitial(message *InitialMessage, sender PublicIdentity) (*ReceiveResult, error) {
	return r.AcceptInitialContext(context.Background(), message, sender)
}

// AcceptInitialContext is AcceptInitial with a context for proof verification.
func (r *Receiver) AcceptInitialContext(ctx context.Context, message *InitialMessage, sender PublicIdentity) (*ReceiveResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if message == nil {
		return nil, fmt.Errorf("%w: nil message", ErrInvalidMessage)
	}
	if err := sender.Validate(); err != nil {
		return nil, err
	}
	if !message.Sender.Equal(sender) {
		return nil, fmt.Errorf("%w: message sender is not the registered sender", ErrSenderMismatch)
	}
	receiverIdentity := r.identity.Public()
	if message.RecipientAccountID != receiverIdentity.AccountID || message.RecipientDeviceID != receiverIdentity.DeviceID {
		return nil, fmt.Errorf("%w: %s/%s", ErrRecipientMismatch, message.RecipientAccountID, message.RecipientDeviceID)
	}
	if err := message.Validate(); err != nil {
		return nil, err
	}
	transcript, err := ParseTranscript(message.Transcript)
	if err != nil {
		return nil, err
	}
	if !transcript.Initiator.Equal(sender) {
		return nil, fmt.Errorf("%w: transcript sender mismatch", ErrSenderMismatch)
	}
	if !transcript.Receiver.Equal(receiverIdentity) {
		return nil, fmt.Errorf("%w: transcript receiver mismatch", ErrRecipientMismatch)
	}

	ephemeralKey, err := parseP256Public(transcript.EphemeralKey)
	if err != nil {
		return nil, err
	}
	if transcript.SignedPreKeyID != r.signedPreKey.ID {
		return nil, fmt.Errorf("%w: %d", ErrUnknownSignedPreKey, transcript.SignedPreKeyID)
	}
	if !bytes.Equal(transcript.SignedPreKey, r.signedPreKey.PublicKey().Bytes()) {
		return nil, fmt.Errorf("%w: signed prekey point", ErrTranscriptMismatch)
	}
	if !transcript.BundleExpiresAt.Equal(r.expiresAt) {
		return nil, fmt.Errorf("%w: bundle expiry", ErrTranscriptMismatch)
	}

	var opk *OneTimePreKey
	if transcript.HasOneTimePreKey {
		if transcript.OneTimePreKeyID == 0 {
			return nil, fmt.Errorf("%w: missing one-time prekey ID", ErrInvalidTranscript)
		}
		r.mu.Lock()
		opk = r.oneTimePreKeys[transcript.OneTimePreKeyID]
		r.mu.Unlock()
		if opk == nil {
			return nil, fmt.Errorf("%w: %d", ErrUnknownOneTimePreKey, transcript.OneTimePreKeyID)
		}
		if !bytes.Equal(transcript.OneTimePreKey, opk.PublicKey().Bytes()) {
			return nil, fmt.Errorf("%w: one-time prekey point", ErrTranscriptMismatch)
		}
	} else if transcript.OneTimePreKeyID != 0 || len(transcript.OneTimePreKey) != 0 {
		return nil, fmt.Errorf("%w: one-time prekey presence", ErrTranscriptMismatch)
	}

	proofDigest := sha256.Sum256(message.DirectoryProof)
	if subtle.ConstantTimeCompare(proofDigest[:], transcript.DirectoryProofDigest[:]) != 1 {
		return nil, fmt.Errorf("%w: proof digest", ErrDirectoryProof)
	}

	expected := Transcript{
		Version:              ProtocolVersion,
		Initiator:            sender.clone(),
		Receiver:             receiverIdentity.clone(),
		EphemeralKey:         append([]byte(nil), transcript.EphemeralKey...),
		SignedPreKeyID:       r.signedPreKey.ID,
		SignedPreKey:         r.signedPreKey.PublicKey().Bytes(),
		HasOneTimePreKey:     opk != nil,
		BundleExpiresAt:      r.expiresAt,
		DirectoryProofDigest: proofDigest,
	}
	if opk != nil {
		expected.OneTimePreKeyID = opk.ID
		expected.OneTimePreKey = opk.PublicKey().Bytes()
	}
	if !bytes.Equal(expected.encode(), message.Transcript) {
		return nil, fmt.Errorf("%w: transcript fields", ErrTranscriptMismatch)
	}
	if !ed25519.Verify(sender.SigningKey, initiatorSignatureInput(message.Transcript), message.Signature) {
		return nil, ErrInvalidSignature
	}

	messageBytes, err := message.MarshalBinary()
	if err != nil {
		return nil, err
	}
	messageDigest := sha256.Sum256(messageBytes)
	transcriptHash := transcript.Hash()

	r.mu.Lock()
	if cached := r.sessions[transcriptHash]; cached != nil {
		if subtle.ConstantTimeCompare(cached.messageDigest[:], messageDigest[:]) == 1 {
			result := cached.result.clone()
			result.Replayed = true
			r.mu.Unlock()
			return result, nil
		}
		r.mu.Unlock()
		return nil, ErrHandshakeConflict
	}
	r.mu.Unlock()

	if !r.now().Before(transcript.BundleExpiresAt) {
		return nil, fmt.Errorf("%w: expiry %s", ErrBundleExpired, transcript.BundleExpiresAt.UTC().Format(time.RFC3339Nano))
	}

	if r.verifyProof != nil {
		if err := r.verifyProof(ctx, receiverIdentity, message.DirectoryProof); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDirectoryProof, err)
		}
	}

	dh1, err := ecdhSecret(r.identity.identityPrivate, ephemeralKey)
	if err != nil {
		return nil, err
	}
	dh2, err := ecdhSecret(r.signedPreKey.PrivateKey, ephemeralKey)
	if err != nil {
		return nil, err
	}
	dh3, err := ecdhSecret(r.signedPreKey.PrivateKey, sender.IdentityKey)
	if err != nil {
		return nil, err
	}
	secrets := [][]byte{dh1, dh2, dh3}
	if opk != nil {
		dh4, err := ecdhSecret(opk.PrivateKey, ephemeralKey)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, dh4)
	}
	rootKey, err := deriveRootKey(secrets, transcriptHash)
	if err != nil {
		return nil, err
	}
	plaintext, err := openAEAD(rootKey[:], message.Nonce, message.Ciphertext, message.Transcript)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if cached := r.sessions[transcriptHash]; cached != nil {
		if subtle.ConstantTimeCompare(cached.messageDigest[:], messageDigest[:]) == 1 {
			result := cached.result.clone()
			result.Replayed = true
			return result, nil
		}
		return nil, ErrHandshakeConflict
	}
	if opk != nil {
		if r.usedOPKs[opk.ID] {
			return nil, fmt.Errorf("%w: %d", ErrOneTimePreKeyUsed, opk.ID)
		}
		r.usedOPKs[opk.ID] = true
	}
	mode := PreKeyModeSignedPreKeyOnly
	if opk != nil {
		mode = PreKeyModeOneTimePreKey
	}
	result := &ReceiveResult{
		Plaintext:         append([]byte(nil), plaintext...),
		RootKey:           append([]byte(nil), rootKey[:]...),
		Transcript:        append([]byte(nil), message.Transcript...),
		TranscriptHash:    transcriptHash,
		OneTimePreKeyUsed: opk != nil,
		Mode:              mode,
	}
	if opk != nil {
		result.OneTimePreKeyID = opk.ID
	}
	r.sessions[transcriptHash] = &receiverSession{
		messageDigest: messageDigest,
		result:        result.clone(),
	}
	return result, nil
}

// OPKUsed reports whether an OPK has already been consumed.
func (r *Receiver) OPKUsed(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usedOPKs[id]
}

// AcceptedHandshakeCount reports the number of distinct first messages accepted.
func (r *Receiver) AcceptedHandshakeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}
