package passkey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Sentinel errors. They are stable so HTTP (or other) transports can map them
// to status codes without parsing messages.
var (
	ErrChallengeNotFound    = errors.New("passkey: challenge not found")
	ErrChallengeExpired     = errors.New("passkey: challenge expired")
	ErrChallengeAlreadyUsed = errors.New("passkey: challenge already consumed")
	ErrMalformedAssertion   = errors.New("passkey: malformed assertion")
	ErrRPIDMismatch         = errors.New("passkey: relying party id mismatch")
	ErrOriginMismatch       = errors.New("passkey: origin mismatch")
	ErrChallengeMismatch    = errors.New("passkey: challenge mismatch")
	ErrUnknownCredential    = errors.New("passkey: unknown credential")
	ErrUserMismatch         = errors.New("passkey: credential does not belong to user")
	ErrInvalidClientData    = errors.New("passkey: invalid client data")
	ErrInvalidSignature     = errors.New("passkey: invalid ES256 signature")
	ErrBackupFlagMismatch   = errors.New("passkey: backup flag does not match registration")
	ErrCounterReplay        = errors.New("passkey: signature counter did not advance (possible cloned authenticator)")
)

const (
	// authData layout: rpIdHash(32) || flags(1) || signCount(4) [|| attData...]
	authDataMinLen = 37
	flagUP         = 1 << 0 // user present
	flagUV         = 1 << 2 // user verified
	flagBE         = 1 << 3 // backup eligible
	flagBS         = 1 << 4 // backup state
	flagAT         = 1 << 6 // attestation data present

	es256SigLen = 64
)

// RelyingParty pins the expected RP ID and origin. Both are required for every
// assertion, which prevents a challenge minted for one RP/origin being
// replayed against another.
type RelyingParty struct {
	ID     string
	Origin string
}

// CounterOutcome classifies how the authenticator's signature counter was
// handled. It lets callers distinguish a backup-credential zero counter from a
// single-device fallback rather than treating both as ordinary increments.
type CounterOutcome int

const (
	// CounterAdvanced is the normal single-device case: signCount strictly
	// increased and the stored value was updated.
	CounterAdvanced CounterOutcome = iota
	// CounterBackupZero is a backup-eligible (synced) credential that reported
	// a zero counter. This is permitted by the WebAuthn spec and is not a clone.
	CounterBackupZero
	// CounterBackupAdvisory is a backup-eligible credential whose counter moved
	// or stayed at zero; the value is best-effort and cannot detect cloning.
	CounterBackupAdvisory
	// CounterSingleDeviceFallback is a non-backup credential that reported zero
	// on both sides. The authenticator is known not to maintain a counter, so
	// the assertion is accepted but clone detection is unavailable.
	CounterSingleDeviceFallback
)

func (c CounterOutcome) String() string {
	switch c {
	case CounterAdvanced:
		return "advanced"
	case CounterBackupZero:
		return "backup-zero"
	case CounterBackupAdvisory:
		return "backup-advisory"
	case CounterSingleDeviceFallback:
		return "single-device-fallback"
	default:
		return "unknown"
	}
}

// AssertionInput is the login-complete request delivered by the client. All
// byte fields are standard base64 (RFC 4648).
type AssertionInput struct {
	CredentialID      string `json:"credentialId"`
	ChallengeID       string `json:"challengeId"`
	AuthenticatorData string `json:"authenticatorData"`
	ClientDataJSON    string `json:"clientDataJSON"`
	Signature         string `json:"signature"`
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// LoginResult is returned once an assertion has been fully verified and the
// challenge has been consumed.
type LoginResult struct {
	CredentialID []byte
	UserID       []byte
	SignCount    uint32
	Counter      CounterOutcome
	SessionToken string
}

// Service wires the stores together and implements the login-complete flow.
type Service struct {
	RP          RelyingParty
	Challenges  *ChallengeStore
	Credentials *CredentialStore
	Sessions    *SessionStore
	// Now is overridable for tests of expiry.
	Now func() time.Time
	// NewToken generates the opaque session token. Overridable in tests.
	NewToken func() (string, error)
}

func (svc *Service) now() time.Time {
	if svc.Now != nil {
		return svc.Now()
	}
	return time.Now()
}

func (svc *Service) newToken() (string, error) {
	if svc.NewToken != nil {
		return svc.NewToken()
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// FinishAssertion verifies an ES256 login assertion and consumes its challenge
// exactly once.
//
// All cryptographic and binding checks run before the challenge is deleted, so
// a bad signature or counter replay never burns the challenge. The final
// delete is an atomic map operation under the store lock, guaranteeing that
// concurrent assertions sharing one challenge produce at most one session.
func (svc *Service) FinishAssertion(in AssertionInput) (*LoginResult, error) {
	credID, err := base64.StdEncoding.DecodeString(in.CredentialID)
	if err != nil || len(credID) == 0 {
		return nil, fmt.Errorf("%w: credential id", ErrMalformedAssertion)
	}
	authData, err := base64.StdEncoding.DecodeString(in.AuthenticatorData)
	if err != nil {
		return nil, fmt.Errorf("%w: authenticator data", ErrMalformedAssertion)
	}
	clientJSON, err := base64.StdEncoding.DecodeString(in.ClientDataJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: client data", ErrMalformedAssertion)
	}
	sig, err := base64.StdEncoding.DecodeString(in.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature", ErrMalformedAssertion)
	}

	// Non-destructive lookups first: an invalid assertion must leave the
	// pending challenge in place for a retry.
	pending, err := svc.Challenges.Peek(in.ChallengeID)
	if err != nil {
		return nil, err
	}
	cred, ok := svc.Credentials.Get(credID)
	if !ok {
		return nil, ErrUnknownCredential
	}

	if pending.RPID != svc.RP.ID || cred.RPID != svc.RP.ID {
		return nil, ErrRPIDMismatch
	}
	if pending.Origin != svc.RP.Origin {
		return nil, ErrOriginMismatch
	}

	var cd clientData
	if err := json.Unmarshal(clientJSON, &cd); err != nil {
		return nil, fmt.Errorf("%w: json", ErrInvalidClientData)
	}
	if cd.Type != "webauthn.get" || cd.CrossOrigin {
		return nil, ErrInvalidClientData
	}
	if cd.Origin != svc.RP.Origin || cd.Origin != pending.Origin {
		return nil, ErrOriginMismatch
	}
	cdChallenge, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil {
		// Some clients emit padded standard base64; accept both.
		cdChallenge, err = base64.StdEncoding.DecodeString(cd.Challenge)
		if err != nil {
			return nil, fmt.Errorf("%w: challenge encoding", ErrInvalidClientData)
		}
	}
	if !bytes.Equal(cdChallenge, pending.Challenge) {
		return nil, ErrChallengeMismatch
	}

	if len(pending.UserID) > 0 && !bytes.Equal(cred.UserID, pending.UserID) {
		return nil, ErrUserMismatch
	}

	rpIDHash := sha256.Sum256([]byte(svc.RP.ID))
	if len(authData) < authDataMinLen {
		return nil, fmt.Errorf("%w: authenticator data too short", ErrMalformedAssertion)
	}
	if !bytes.Equal(authData[:32], rpIDHash[:]) {
		return nil, ErrRPIDMismatch
	}
	flags := authData[32]
	if flags&flagUP == 0 {
		return nil, fmt.Errorf("%w: user present flag not set", ErrMalformedAssertion)
	}
	backup := flags&flagBE != 0
	if backup != cred.BackupEligible {
		return nil, ErrBackupFlagMismatch
	}
	newCount := binary.BigEndian.Uint32(authData[33:37])

	pub, err := unmarshalES256Key(cred.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: public key", ErrMalformedAssertion)
	}

	// signedData = authenticatorData || sha256(clientDataJSON)
	clientHash := sha256.Sum256(clientJSON)
	signedData := make([]byte, 0, len(authData)+sha256.Size)
	signedData = append(signedData, authData...)
	signedData = append(signedData, clientHash[:]...)
	sigHash := sha256.Sum256(signedData)
	if len(sig) != es256SigLen {
		return nil, fmt.Errorf("%w: length", ErrInvalidSignature)
	}
	r := new(big.Int)
	s := new(big.Int)
	r.SetBytes(sig[:32])
	s.SetBytes(sig[32:])
	if !ecdsa.Verify(pub, sigHash[:], r, s) {
		return nil, ErrInvalidSignature
	}

	outcome, err := svc.applyCounter(cred, newCount, backup)
	if err != nil {
		return nil, err
	}

	// All checks passed. Atomically burn the challenge: only the first caller
	// wins the map delete under the store lock.
	if _, err := svc.Challenges.Consume(in.ChallengeID); err != nil {
		return nil, err
	}

	token, err := svc.newToken()
	if err != nil {
		return nil, fmt.Errorf("passkey: session token: %w", err)
	}
	svc.Sessions.Add(Session{
		Token:        token,
		CredentialID: cloneBytes(cred.ID),
		UserID:       cloneBytes(cred.UserID),
		CreatedAt:    svc.now(),
	})

	return &LoginResult{
		CredentialID: cloneBytes(cred.ID),
		UserID:       cloneBytes(cred.UserID),
		SignCount:    newCount,
		Counter:      outcome,
		SessionToken: token,
	}, nil
}

// applyCounter implements the WebAuthn counter rules and distinguishes the
// backup zero-counter case from the single-device zero-counter fallback.
func (svc *Service) applyCounter(cred Credential, newCount uint32, backup bool) (CounterOutcome, error) {
	stored := cred.SignCount

	if backup {
		// Backup-eligible/synced passkeys are allowed to keep a zero counter and
		// their value cannot be used for clone detection. Record it best-effort.
		if newCount == 0 {
			return CounterBackupZero, nil
		}
		svc.Credentials.SetCountIfGreater(cred.ID, newCount)
		return CounterBackupAdvisory, nil
	}

	if stored == 0 && newCount == 0 {
		// A non-synced authenticator that never had a counter: this is the
		// single-device fallback. Accept without clone-detection guarantees.
		return CounterSingleDeviceFallback, nil
	}
	if newCount <= stored {
		// A real counter that failed to advance normally signals a possible
		// clone. During a concurrent duplicate-assertion race the winner may
		// have already advanced the stored counter; treat this as a replay so a
		// standalone assertion is rejected, while the atomic challenge Consume
		// remains the single authority that lets exactly one racy request win.
		return 0, ErrCounterReplay
	}
	if !svc.Credentials.CompareAndSwapCount(cred.ID, stored, newCount) {
		// Another request advanced it first; the assertion was valid, and the
		// challenge consumption below still enforces single-session semantics.
		return CounterAdvanced, nil
	}
	return CounterAdvanced, nil
}

// unmarshalES256Key decodes a raw P-256 uncompressed point into an ECDSA key.
func unmarshalES256Key(raw []byte) (*ecdsa.PublicKey, error) {
	curve := elliptic.P256()
	if len(raw) != 65 || raw[0] != 4 {
		return nil, errors.New("not an uncompressed P-256 point")
	}
	x := new(big.Int)
	y := new(big.Int)
	x.SetBytes(raw[1:33])
	y.SetBytes(raw[33:65])
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("point not on P-256")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}
