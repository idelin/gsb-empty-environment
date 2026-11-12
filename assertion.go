package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	// DefaultChallengeTTL is the maximum age of a server challenge.
	DefaultChallengeTTL = 60 * time.Second

	rpIDHashLen          = 32
	flagsOffset          = 32
	counterOffset        = 33
	counterLen           = 4
	authenticatorDataLen = rpIDHashLen + 1 + counterLen

	challengeEntropyLen = 32
)

// Assertion is the reusable App Attest assertion material supplied by the
// client.
type Assertion struct {
	// AuthenticatorData must be 37 bytes: SHA-256(teamID + "." + bundleID),
	// one flags byte equal to 0x00, and a big-endian uint32 counter.
	AuthenticatorData []byte
	// ClientDataHash must be SHA-256(challenge || SHA-256(requestBody)).
	ClientDataHash []byte
	// Signature is a DER-encoded ECDSA P-256 signature over
	// SHA-256(AuthenticatorData || ClientDataHash).
	Signature []byte
}

// VerifyRequest contains everything needed to authenticate one request.
type VerifyRequest struct {
	// KeyID identifies the App Attest key whose counter is stored.
	KeyID string
	// PublicKey is the public key previously registered for KeyID.
	PublicKey *ecdsa.PublicKey
	// Challenge is the one-time challenge issued by the server.
	Challenge string
	// Body is the exact request body covered by the assertion.
	Body []byte
	// Assertion is the assertion received from the client.
	Assertion Assertion
}

// Verifier validates App Attest assertions.
//
// TeamID and BundleID are used to derive the required rpIdHash. Counters and
// Challenges may be backed by the same Store or by separate implementations.
// Now may be replaced in tests; it must not be changed concurrently with
// Verify or IssueChallenge. A nil Now uses time.Now.
type Verifier struct {
	TeamID     string
	BundleID   string
	Counters   CounterStore
	Challenges ChallengeStore
	Now        func() time.Time
}

// NewVerifier creates a Verifier backed by a single Store.
func NewVerifier(teamID, bundleID string, store Store) (*Verifier, error) {
	if store == nil {
		return nil, ErrNilStore
	}
	return NewVerifierWithStores(teamID, bundleID, store, store)
}

// NewVerifierWithStores creates a Verifier with separate counter and
// challenge persistence.
func NewVerifierWithStores(teamID, bundleID string, counters CounterStore, challenges ChallengeStore) (*Verifier, error) {
	if teamID == "" {
		return nil, ErrEmptyTeamID
	}
	if bundleID == "" {
		return nil, ErrEmptyBundleID
	}
	if counters == nil {
		return nil, ErrNilCounterStore
	}
	if challenges == nil {
		return nil, ErrNilChallengeStore
	}
	return &Verifier{
		TeamID:     teamID,
		BundleID:   bundleID,
		Counters:   counters,
		Challenges: challenges,
	}, nil
}

// IssueChallenge generates a high-entropy, single-use challenge that expires
// after DefaultChallengeTTL.
func (v *Verifier) IssueChallenge() (string, error) {
	if v == nil {
		return "", ErrNilVerifier
	}
	if v.TeamID == "" {
		return "", ErrEmptyTeamID
	}
	if v.BundleID == "" {
		return "", ErrEmptyBundleID
	}
	if v.Challenges == nil {
		return "", ErrNilChallengeStore
	}

	var entropy [challengeEntropyLen]byte
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", fmt.Errorf("%w: %v", ErrChallengeGeneration, err)
		}
		challenge := base64.RawURLEncoding.EncodeToString(entropy[:])
		expiresAt := v.currentTime().Add(DefaultChallengeTTL)
		err := v.Challenges.PutChallenge(challenge, expiresAt)
		if err == nil {
			return challenge, nil
		}
		if !errors.Is(err, ErrChallengeDuplicate) {
			return "", fmt.Errorf("netsec: store challenge: %w", err)
		}
	}
	return "", fmt.Errorf("%w: repeated duplicate challenge", ErrChallengeGeneration)
}

// Verify validates one assertion and, only if every check passes, atomically
// advances the counter for req.KeyID.
//
// Check order: request shape, challenge consumption, rpIdHash, flags,
// clientDataHash, ECDSA signature, stored counter, atomic counter update. The
// challenge is consumed before the cryptographic checks; every failure before
// the final compare-and-swap leaves the counter unchanged.
func (v *Verifier) Verify(req VerifyRequest) error {
	if v == nil {
		return ErrNilVerifier
	}
	if v.TeamID == "" {
		return ErrEmptyTeamID
	}
	if v.BundleID == "" {
		return ErrEmptyBundleID
	}
	if v.Counters == nil {
		return ErrNilCounterStore
	}
	if v.Challenges == nil {
		return ErrNilChallengeStore
	}
	if req.KeyID == "" {
		return ErrEmptyKeyID
	}
	if req.Challenge == "" {
		return ErrEmptyChallenge
	}
	if err := validateP256PublicKey(req.PublicKey); err != nil {
		return err
	}

	authenticatorData := req.Assertion.AuthenticatorData
	if len(authenticatorData) != authenticatorDataLen {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidAuthenticatorData, len(authenticatorData), authenticatorDataLen)
	}
	clientDataHash := req.Assertion.ClientDataHash
	if len(clientDataHash) != sha256.Size {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidClientDataHash, len(clientDataHash), sha256.Size)
	}
	if len(req.Assertion.Signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrInvalidSignature)
	}

	// Consume the challenge before doing any cryptographic work. A caller
	// that retries after a failure must obtain a fresh challenge.
	if err := v.Challenges.ConsumeChallenge(req.Challenge, v.currentTime()); err != nil {
		return err
	}

	expectedRPIDHash := sha256.Sum256([]byte(v.TeamID + "." + v.BundleID))
	if subtle.ConstantTimeCompare(authenticatorData[:rpIDHashLen], expectedRPIDHash[:]) != 1 {
		return ErrRPIDHashMismatch
	}
	if authenticatorData[flagsOffset] != 0x00 {
		return fmt.Errorf("%w: got 0x%02x", ErrInvalidFlags, authenticatorData[flagsOffset])
	}

	counter := binary.BigEndian.Uint32(authenticatorData[counterOffset : counterOffset+counterLen])

	bodyHash := sha256.Sum256(req.Body)
	challengeInput := make([]byte, 0, len(req.Challenge)+sha256.Size)
	challengeInput = append(challengeInput, req.Challenge...)
	challengeInput = append(challengeInput, bodyHash[:]...)
	expectedClientDataHash := sha256.Sum256(challengeInput)
	if subtle.ConstantTimeCompare(clientDataHash, expectedClientDataHash[:]) != 1 {
		return ErrClientDataHashMismatch
	}

	signatureInput := make([]byte, 0, len(authenticatorData)+sha256.Size)
	signatureInput = append(signatureInput, authenticatorData...)
	signatureInput = append(signatureInput, expectedClientDataHash[:]...)
	signatureDigest := sha256.Sum256(signatureInput)
	if !ecdsa.VerifyASN1(req.PublicKey, signatureDigest[:], req.Assertion.Signature) {
		return ErrInvalidSignature
	}

	previous, found, err := v.Counters.Counter(req.KeyID)
	if err != nil {
		return fmt.Errorf("netsec: read counter: %w", err)
	}
	if !found {
		previous = 0
	}
	if counter <= previous {
		return fmt.Errorf("%w: assertion counter %d, recorded counter %d", ErrCounterNotAdvanced, counter, previous)
	}

	swapped, err := v.Counters.CompareAndSwapCounter(req.KeyID, previous, counter)
	if err != nil {
		return fmt.Errorf("netsec: update counter: %w", err)
	}
	if !swapped {
		return ErrCounterConflict
	}
	return nil
}

func (v *Verifier) currentTime() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func validateP256PublicKey(publicKey *ecdsa.PublicKey) error {
	if publicKey == nil ||
		publicKey.Curve != elliptic.P256() ||
		publicKey.X == nil ||
		publicKey.Y == nil ||
		!publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return ErrInvalidPublicKey
	}
	return nil
}
