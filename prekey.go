package netsec

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Curve25519KeyType is the type byte used when a Curve25519 public key is
// serialized for a Signal-style prekey message. The type byte is part of the
// signed payload, not merely transport metadata.
const Curve25519KeyType byte = 0x05

// KeyTypeCurve25519 is an alias for Curve25519KeyType.
const KeyTypeCurve25519 byte = Curve25519KeyType

var (
	ErrEmptyUserID                  = errors.New("netsec: empty user ID")
	ErrUserNotFound                 = errors.New("netsec: user not found")
	ErrInvalidIdentityKey           = errors.New("netsec: invalid identity key")
	ErrIdentityKeyMismatch          = errors.New("netsec: identity key does not match registration")
	ErrInvalidSignedPreKey          = errors.New("netsec: invalid signed prekey")
	ErrInvalidSignedPreKeySignature = errors.New("netsec: invalid signed prekey signature")
	ErrInvalidOneTimePreKey         = errors.New("netsec: invalid one-time prekey")
	ErrDuplicateOneTimePreKey       = errors.New("netsec: duplicate one-time prekey")
	ErrInvalidIdentityKeyArgument   = errors.New("netsec: invalid identity key argument")
)

// Aliases keep the error names convenient for callers that use shorter names.
var (
	ErrIdentityMismatch       = ErrIdentityKeyMismatch
	ErrNoUser                 = ErrUserNotFound
	ErrInvalidSignedPreKeySig = ErrInvalidSignedPreKeySignature
)

// SignedPreKey is a medium-term prekey. Signature is an Ed25519 signature made
// by the user's identity key over EncodePublicKey(PublicKey).
type SignedPreKey struct {
	ID        uint32
	PublicKey []byte
	Signature []byte
}

// OneTimePreKey is consumed by at most one prekey bundle request.
type OneTimePreKey struct {
	ID        uint32
	PublicKey []byte
}

// PreKey is an alias for OneTimePreKey.
type PreKey = OneTimePreKey

// PreKeyBundle is the material returned to a session initiator. OneTimePreKey
// is nil when the user has no one-time prekeys left.
type PreKeyBundle struct {
	IdentityKey   ed25519.PublicKey
	SignedPreKey  SignedPreKey
	OneTimePreKey *OneTimePreKey
}

// HasOneTimePreKey reports whether the bundle includes a one-time prekey.
func (b *PreKeyBundle) HasOneTimePreKey() bool {
	return b != nil && b.OneTimePreKey != nil
}

// EncodePublicKey serializes a public key with the Curve25519 type byte. The
// returned slice is a copy.
func EncodePublicKey(publicKey []byte) []byte {
	encoded := make([]byte, len(publicKey)+1)
	encoded[0] = Curve25519KeyType
	copy(encoded[1:], publicKey)
	return encoded
}

// EncodePublicKeyWithType is an explicit alias for EncodePublicKey.
func EncodePublicKeyWithType(publicKey []byte) []byte {
	return EncodePublicKey(publicKey)
}

// SignedPreKeyMessage returns the exact bytes covered by the signed prekey
// signature.
func SignedPreKeyMessage(signedPreKey SignedPreKey) []byte {
	return EncodePublicKey(signedPreKey.PublicKey)
}

// SignSignedPreKey signs a signed prekey public key with an Ed25519 identity
// private key. The signature covers the encoded key, including its type byte.
func SignSignedPreKey(identityPrivateKey ed25519.PrivateKey, publicKey []byte) ([]byte, error) {
	if len(identityPrivateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidIdentityKey
	}
	if len(publicKey) == 0 {
		return nil, ErrInvalidSignedPreKey
	}
	return ed25519.Sign(identityPrivateKey, EncodePublicKey(publicKey)), nil
}

// VerifySignedPreKey verifies a signed prekey against an identity public key.
// It returns false for malformed keys or signatures.
func VerifySignedPreKey(identityKey ed25519.PublicKey, signedPreKey SignedPreKey) bool {
	if len(identityKey) != ed25519.PublicKeySize || len(signedPreKey.PublicKey) == 0 {
		return false
	}
	return ed25519.Verify(identityKey, SignedPreKeyMessage(signedPreKey), signedPreKey.Signature)
}

// Store holds per-user identity, signed-prekey, and one-time-prekey state.
// Fetching a bundle is the only operation that consumes one-time prekeys.
type Store struct {
	mu       sync.Mutex
	accounts map[string]*account
}

type account struct {
	identity     ed25519.PublicKey
	signedPreKey SignedPreKey
	oneTime      []OneTimePreKey
	seenIDs      map[uint32]struct{}
	seenKeys     map[string]struct{}
}

// NewStore returns an empty prekey store.
func NewStore() *Store {
	return &Store{accounts: make(map[string]*account)}
}

// NewPreKeyStore is an alias for NewStore.
func NewPreKeyStore() *Store {
	return NewStore()
}

// Register creates or updates a user's prekey material. A user's identity key
// is immutable: registering a different identity key for an existing user is
// rejected and leaves the account untouched. One-time prekeys are additive.
func (s *Store) Register(userID string, identityKey ed25519.PublicKey, signedPreKey SignedPreKey, oneTimePreKeys []OneTimePreKey) error {
	if strings.TrimSpace(userID) == "" {
		return ErrEmptyUserID
	}
	if len(identityKey) != ed25519.PublicKeySize {
		return ErrInvalidIdentityKey
	}
	if len(signedPreKey.PublicKey) == 0 || len(signedPreKey.Signature) == 0 {
		return ErrInvalidSignedPreKey
	}
	if err := validateOneTimePreKeys(oneTimePreKeys); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.accounts == nil {
		s.accounts = make(map[string]*account)
	}

	acct := s.accounts[userID]
	if acct != nil && !identityKeysEqual(acct.identity, identityKey) {
		return ErrIdentityKeyMismatch
	}
	if !VerifySignedPreKey(identityKey, signedPreKey) {
		return ErrInvalidSignedPreKeySig
	}
	if acct == nil {
		acct = &account{
			identity: append(ed25519.PublicKey(nil), identityKey...),
			seenIDs:  make(map[uint32]struct{}),
			seenKeys: make(map[string]struct{}),
		}
	} else if err := acct.checkNewOneTimePreKeys(oneTimePreKeys); err != nil {
		return err
	}

	acct.signedPreKey = cloneSignedPreKey(signedPreKey)
	if acct.seenIDs == nil {
		acct.seenIDs = make(map[uint32]struct{})
	}
	if acct.seenKeys == nil {
		acct.seenKeys = make(map[string]struct{})
	}
	acct.addOneTimePreKeys(oneTimePreKeys)
	s.accounts[userID] = acct
	return nil
}

// RegisterUser is an alias for Register.
func (s *Store) RegisterUser(userID string, identityKey ed25519.PublicKey, signedPreKey SignedPreKey, oneTimePreKeys []OneTimePreKey) error {
	return s.Register(userID, identityKey, signedPreKey, oneTimePreKeys)
}

// AddOneTimePreKeys adds a batch of one-time prekeys to an existing user.
// Duplicate IDs or duplicate public keys are rejected atomically.
func (s *Store) AddOneTimePreKeys(userID string, oneTimePreKeys []OneTimePreKey) error {
	if strings.TrimSpace(userID) == "" {
		return ErrEmptyUserID
	}
	if err := validateOneTimePreKeys(oneTimePreKeys); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acct := s.accounts[userID]
	if acct == nil {
		return ErrUserNotFound
	}
	if err := acct.checkNewOneTimePreKeys(oneTimePreKeys); err != nil {
		return err
	}
	acct.addOneTimePreKeys(oneTimePreKeys)
	return nil
}

// RotateSignedPreKey replaces a user's signed prekey, provided the caller can
// prove the same identity key that was pinned at registration.
func (s *Store) RotateSignedPreKey(userID string, identityKey ed25519.PublicKey, signedPreKey SignedPreKey) error {
	if strings.TrimSpace(userID) == "" {
		return ErrEmptyUserID
	}
	if len(identityKey) != ed25519.PublicKeySize {
		return ErrInvalidIdentityKey
	}
	if len(signedPreKey.PublicKey) == 0 || len(signedPreKey.Signature) == 0 {
		return ErrInvalidSignedPreKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acct := s.accounts[userID]
	if acct == nil {
		return ErrUserNotFound
	}
	if !identityKeysEqual(acct.identity, identityKey) {
		return ErrIdentityKeyMismatch
	}
	if !VerifySignedPreKey(identityKey, signedPreKey) {
		return ErrInvalidSignedPreKeySig
	}
	acct.signedPreKey = cloneSignedPreKey(signedPreKey)
	return nil
}

// FetchPreKeyBundle returns the identity key, the current signed prekey, and
// at most one one-time prekey. The one-time prekey is removed from the pool
// while holding the store lock, so concurrent callers cannot receive the same
// prekey. If the pool is empty, OneTimePreKey is nil and no error is returned.
//
// An optional identity key may be supplied by the session initiator. When
// present, it must match the identity key pinned at registration; a mismatch
// is rejected before any one-time prekey is consumed.
func (s *Store) FetchPreKeyBundle(userID string, claimedIdentityKey ...ed25519.PublicKey) (*PreKeyBundle, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrEmptyUserID
	}
	if len(claimedIdentityKey) > 1 {
		return nil, ErrInvalidIdentityKeyArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acct := s.accounts[userID]
	if acct == nil {
		return nil, ErrUserNotFound
	}
	if len(claimedIdentityKey) == 1 {
		claimed := claimedIdentityKey[0]
		if len(claimed) != ed25519.PublicKeySize {
			return nil, ErrInvalidIdentityKey
		}
		if !identityKeysEqual(acct.identity, claimed) {
			return nil, ErrIdentityKeyMismatch
		}
	}

	bundle := &PreKeyBundle{
		IdentityKey:  append(ed25519.PublicKey(nil), acct.identity...),
		SignedPreKey: cloneSignedPreKey(acct.signedPreKey),
	}
	if len(acct.oneTime) > 0 {
		oneTime := acct.oneTime[0]
		acct.oneTime[0] = OneTimePreKey{}
		acct.oneTime = acct.oneTime[1:]
		bundle.OneTimePreKey = &OneTimePreKey{
			ID:        oneTime.ID,
			PublicKey: append([]byte(nil), oneTime.PublicKey...),
		}
	}
	return bundle, nil
}

// GetPreKeyBundle is an alias for FetchPreKeyBundle.
func (s *Store) GetPreKeyBundle(userID string, claimedIdentityKey ...ed25519.PublicKey) (*PreKeyBundle, error) {
	return s.FetchPreKeyBundle(userID, claimedIdentityKey...)
}

// FetchBundle is an alias for FetchPreKeyBundle.
func (s *Store) FetchBundle(userID string, claimedIdentityKey ...ed25519.PublicKey) (*PreKeyBundle, error) {
	return s.FetchPreKeyBundle(userID, claimedIdentityKey...)
}

// ConsumeBundle is an alias for FetchPreKeyBundle.
func (s *Store) ConsumeBundle(userID string, claimedIdentityKey ...ed25519.PublicKey) (*PreKeyBundle, error) {
	return s.FetchPreKeyBundle(userID, claimedIdentityKey...)
}

// PendingOneTimePreKeyCount returns the number of unconsumed one-time prekeys.
func (s *Store) PendingOneTimePreKeyCount(userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	acct := s.accounts[userID]
	if acct == nil {
		return 0, ErrUserNotFound
	}
	return len(acct.oneTime), nil
}

// HasUser reports whether userID has been registered.
func (s *Store) HasUser(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.accounts[userID]
	return ok
}

func validateOneTimePreKeys(keys []OneTimePreKey) error {
	ids := make(map[uint32]struct{}, len(keys))
	publicKeys := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if len(key.PublicKey) == 0 {
			return ErrInvalidOneTimePreKey
		}
		if _, ok := ids[key.ID]; ok {
			return fmt.Errorf("%w: ID %d", ErrDuplicateOneTimePreKey, key.ID)
		}
		keyBytes := string(key.PublicKey)
		if _, ok := publicKeys[keyBytes]; ok {
			return fmt.Errorf("%w: public key repeated", ErrDuplicateOneTimePreKey)
		}
		ids[key.ID] = struct{}{}
		publicKeys[keyBytes] = struct{}{}
	}
	return nil
}

func (a *account) checkNewOneTimePreKeys(keys []OneTimePreKey) error {
	for _, key := range keys {
		if _, ok := a.seenIDs[key.ID]; ok {
			return fmt.Errorf("%w: ID %d already issued", ErrDuplicateOneTimePreKey, key.ID)
		}
		if _, ok := a.seenKeys[string(key.PublicKey)]; ok {
			return fmt.Errorf("%w: public key already issued", ErrDuplicateOneTimePreKey)
		}
	}
	return nil
}

func (a *account) addOneTimePreKeys(keys []OneTimePreKey) {
	for _, key := range keys {
		a.oneTime = append(a.oneTime, cloneOneTimePreKey(key))
		a.seenIDs[key.ID] = struct{}{}
		a.seenKeys[string(key.PublicKey)] = struct{}{}
	}
}

func cloneSignedPreKey(key SignedPreKey) SignedPreKey {
	return SignedPreKey{
		ID:        key.ID,
		PublicKey: append([]byte(nil), key.PublicKey...),
		Signature: append([]byte(nil), key.Signature...),
	}
}

func cloneOneTimePreKey(key OneTimePreKey) OneTimePreKey {
	return OneTimePreKey{
		ID:        key.ID,
		PublicKey: append([]byte(nil), key.PublicKey...),
	}
}

func identityKeysEqual(a, b ed25519.PublicKey) bool {
	return len(a) == ed25519.PublicKeySize &&
		len(b) == ed25519.PublicKeySize &&
		subtle.ConstantTimeCompare(a, b) == 1
}
