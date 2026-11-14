package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrUnknownKey is returned when a token names a key that the verifier does
// not know about.
var ErrUnknownKey = errors.New("netsec: unknown signing key")

// ErrRetiredKey is returned when a token was signed with a key that has
// explicitly been retired.
var ErrRetiredKey = errors.New("netsec: signing key has been retired")

// ErrWeakKey is returned for asymmetric keys that do not meet the minimum
// strength accepted by this package.
var ErrWeakKey = errors.New("netsec: cryptographic key is too weak")

// ErrUnsupportedAlgorithm is returned for JWS algorithms that are not
// implemented or that do not match the selected key type.
var ErrUnsupportedAlgorithm = errors.New("netsec: unsupported signing algorithm")

type keyEntry struct {
	id        string
	algorithm string
	signer    crypto.Signer
	public    crypto.PublicKey
	retired   bool
}

// KeySet holds one active signing key plus any verification keys that are
// still trusted. It is safe for concurrent use.
//
// A normal key rotation keeps the old public key in the set during the
// overlap window so that already-issued tokens can still be validated. Call
// RetireKey (or RemoveKey) when the overlap window ends; tokens signed by a
// retired key are then rejected. ReplaceSigningKey performs an immediate
// rotation and retires every other key.
type KeySet struct {
	mu     sync.RWMutex
	keys   map[string]*keyEntry
	active string
}

// KeyRing is an alias retained for callers that prefer that terminology.
type KeyRing = KeySet

// NewKeySet returns an empty key set.
func NewKeySet() *KeySet {
	return &KeySet{keys: make(map[string]*keyEntry)}
}

// NewKeyRing returns an empty key set.
func NewKeyRing() *KeySet {
	return NewKeySet()
}

// NewKeySetFromSigner creates a key set containing one active signing key.
func NewKeySetFromSigner(keyID string, signer crypto.Signer) (*KeySet, error) {
	ks := NewKeySet()
	if err := ks.AddSigningKey(keyID, signer); err != nil {
		return nil, err
	}
	return ks, nil
}

// NewKeySetWithSigner is an alias for NewKeySetFromSigner.
func NewKeySetWithSigner(keyID string, signer crypto.Signer) (*KeySet, error) {
	return NewKeySetFromSigner(keyID, signer)
}

// AddSigningKey adds a key that can both sign and verify. If no active key
// exists, the new key becomes active. Existing keys remain trusted for
// verification until they are retired or removed.
func (ks *KeySet) AddSigningKey(keyID string, signer crypto.Signer) error {
	if signer == nil {
		return errors.New("netsec: nil signer")
	}
	return ks.addSigningKey(keyID, signer, "")
}

// AddSigningKeyWithAlgorithm is AddSigningKey with an explicit JWS algorithm.
// It is useful when an RSA key must use PS256 or a non-default RSA hash.
func (ks *KeySet) AddSigningKeyWithAlgorithm(keyID string, signer crypto.Signer, algorithm string) error {
	if signer == nil {
		return errors.New("netsec: nil signer")
	}
	return ks.addSigningKey(keyID, signer, algorithm)
}

func (ks *KeySet) addSigningKey(keyID string, signer crypto.Signer, algorithm string) error {
	if keyID == "" {
		return errors.New("netsec: key id is required")
	}
	public := normalizePublicKey(signer.Public())
	algorithm, err := resolveAlgorithm(public, algorithm)
	if err != nil {
		return err
	}
	if err := checkPublicKey(public); err != nil {
		return err
	}

	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.keys == nil {
		ks.keys = make(map[string]*keyEntry)
	}
	if _, exists := ks.keys[keyID]; exists {
		return fmt.Errorf("netsec: key %q already exists", keyID)
	}
	ks.keys[keyID] = &keyEntry{
		id:        keyID,
		algorithm: algorithm,
		signer:    signer,
		public:    public,
	}
	ks.active = keyID
	return nil
}

// AddVerificationKey adds a public key that may verify tokens but cannot be
// used to sign new ones.
func (ks *KeySet) AddVerificationKey(keyID string, public crypto.PublicKey) error {
	return ks.addVerificationKey(keyID, public, "")
}

// AddVerificationKeyWithAlgorithm is AddVerificationKey with an explicit JWS
// algorithm.
func (ks *KeySet) AddVerificationKeyWithAlgorithm(keyID string, public crypto.PublicKey, algorithm string) error {
	return ks.addVerificationKey(keyID, public, algorithm)
}

func (ks *KeySet) addVerificationKey(keyID string, public crypto.PublicKey, algorithm string) error {
	if keyID == "" {
		return errors.New("netsec: key id is required")
	}
	if public == nil {
		return errors.New("netsec: nil public key")
	}
	public = normalizePublicKey(public)
	algorithm, err := resolveAlgorithm(public, algorithm)
	if err != nil {
		return err
	}
	if err := checkPublicKey(public); err != nil {
		return err
	}

	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.keys == nil {
		ks.keys = make(map[string]*keyEntry)
	}
	if _, exists := ks.keys[keyID]; exists {
		return fmt.Errorf("netsec: key %q already exists", keyID)
	}
	ks.keys[keyID] = &keyEntry{
		id:        keyID,
		algorithm: algorithm,
		public:    public,
	}
	return nil
}

// SetSigningKey adds the key and makes it active.
func (ks *KeySet) SetSigningKey(keyID string, signer crypto.Signer) error {
	return ks.AddSigningKey(keyID, signer)
}

// RotateSigningKey adds a new active signing key while retaining previously
// added verification keys during the overlap period.
func (ks *KeySet) RotateSigningKey(keyID string, signer crypto.Signer) error {
	return ks.AddSigningKey(keyID, signer)
}

// ReplaceSigningKey adds a new active key and immediately retires all other
// keys in the set. Tokens signed with the previous key will no longer verify.
func (ks *KeySet) ReplaceSigningKey(keyID string, signer crypto.Signer) error {
	if err := ks.AddSigningKey(keyID, signer); err != nil {
		return err
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for id, entry := range ks.keys {
		if id != keyID {
			entry.retired = true
		}
	}
	return nil
}

// RotateSigningKeyAndRetirePrevious is an alias for ReplaceSigningKey.
func (ks *KeySet) RotateSigningKeyAndRetirePrevious(keyID string, signer crypto.Signer) error {
	return ks.ReplaceSigningKey(keyID, signer)
}

// RetireKey stops accepting tokens signed with keyID. The key material is
// kept so that callers can inspect the set, but verification always fails.
func (ks *KeySet) RetireKey(keyID string) bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	entry, ok := ks.keys[keyID]
	if !ok {
		return false
	}
	entry.retired = true
	if ks.active == keyID {
		ks.active = ""
	}
	return true
}

// RemoveKey removes a key entirely.
func (ks *KeySet) RemoveKey(keyID string) bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if _, ok := ks.keys[keyID]; !ok {
		return false
	}
	delete(ks.keys, keyID)
	if ks.active == keyID {
		ks.active = ""
	}
	return true
}

// ActiveKeyID returns the key ID used for new signatures, if any.
func (ks *KeySet) ActiveKeyID() string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.active
}

// KeyIDs returns a snapshot of all key IDs in the set.
func (ks *KeySet) KeyIDs() []string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	ids := make([]string, 0, len(ks.keys))
	for id := range ks.keys {
		ids = append(ids, id)
	}
	return ids
}

// HasUsableKey reports whether the set contains at least one non-retired key.
func (ks *KeySet) HasUsableKey() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	for _, entry := range ks.keys {
		if !entry.retired {
			return true
		}
	}
	return false
}

func (ks *KeySet) lookup(keyID, algorithm string) (*keyEntry, error) {
	ks.mu.RLock()
	entry, ok := ks.keys[keyID]
	ks.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownKey
	}
	if entry.retired {
		return nil, ErrRetiredKey
	}
	if !algorithmMatchesKey(algorithm, entry.public) {
		return nil, ErrUnsupportedAlgorithm
	}
	return entry, nil
}

func (ks *KeySet) activeEntry() (*keyEntry, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	if ks.active == "" {
		return nil, errors.New("netsec: no active signing key")
	}
	entry, ok := ks.keys[ks.active]
	if !ok || entry.retired || entry.signer == nil {
		return nil, errors.New("netsec: no active signing key")
	}
	return entry, nil
}

// AlgorithmForPublicKey returns the default JWS algorithm for a key.
func AlgorithmForPublicKey(public crypto.PublicKey) (string, error) {
	public = normalizePublicKey(public)
	switch key := public.(type) {
	case *rsa.PublicKey:
		return "RS256", nil
	case *ecdsa.PublicKey:
		switch key.Curve {
		case elliptic.P256():
			return "ES256", nil
		case elliptic.P384():
			return "ES384", nil
		case elliptic.P521():
			return "ES512", nil
		default:
			return "", fmt.Errorf("%w: unsupported ECDSA curve", ErrUnsupportedAlgorithm)
		}
	case ed25519.PublicKey:
		return "EdDSA", nil
	case *ed25519.PublicKey:
		if key == nil {
			return "", errors.New("netsec: nil public key")
		}
		return "EdDSA", nil
	default:
		return "", fmt.Errorf("%w: %T", ErrUnsupportedAlgorithm, public)
	}
}

func normalizePublicKey(public crypto.PublicKey) crypto.PublicKey {
	switch key := public.(type) {
	case rsa.PublicKey:
		value := key
		return &value
	case ecdsa.PublicKey:
		value := key
		return &value
	case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey, *ed25519.PublicKey:
		return key
	default:
		return public
	}
}

func resolveAlgorithm(public crypto.PublicKey, requested string) (string, error) {
	if requested == "" {
		return AlgorithmForPublicKey(public)
	}
	algorithm, ok := canonicalAlgorithm(requested)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, requested)
	}
	if !algorithmMatchesKey(algorithm, public) {
		return "", fmt.Errorf("%w: %s does not match key type", ErrUnsupportedAlgorithm, algorithm)
	}
	return algorithm, nil
}

func canonicalAlgorithm(algorithm string) (string, bool) {
	switch strings.ToUpper(algorithm) {
	case "RS256":
		return "RS256", true
	case "RS384":
		return "RS384", true
	case "RS512":
		return "RS512", true
	case "PS256":
		return "PS256", true
	case "PS384":
		return "PS384", true
	case "PS512":
		return "PS512", true
	case "ES256":
		return "ES256", true
	case "ES384":
		return "ES384", true
	case "ES512":
		return "ES512", true
	case "EDDSA":
		return "EdDSA", true
	default:
		return "", false
	}
}

func algorithmMatchesKey(algorithm string, public crypto.PublicKey) bool {
	algorithm, ok := canonicalAlgorithm(algorithm)
	if !ok {
		return false
	}
	switch public.(type) {
	case *rsa.PublicKey:
		return strings.HasPrefix(algorithm, "RS") || strings.HasPrefix(algorithm, "PS")
	case *ecdsa.PublicKey:
		key := public.(*ecdsa.PublicKey)
		switch algorithm {
		case "ES256":
			return key.Curve == elliptic.P256()
		case "ES384":
			return key.Curve == elliptic.P384()
		case "ES512":
			return key.Curve == elliptic.P521()
		}
	case ed25519.PublicKey, *ed25519.PublicKey:
		return algorithm == "EdDSA"
	}
	return false
}

func checkPublicKey(public crypto.PublicKey) error {
	switch key := public.(type) {
	case *rsa.PublicKey:
		if key == nil || key.N == nil || key.N.BitLen() < 2048 {
			return fmt.Errorf("%w: RSA keys must be at least 2048 bits", ErrWeakKey)
		}
	case *ecdsa.PublicKey:
		if key == nil || key.Curve == nil {
			return errors.New("netsec: invalid ECDSA key")
		}
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return errors.New("netsec: invalid Ed25519 key")
		}
	case *ed25519.PublicKey:
		if key == nil || len(*key) != ed25519.PublicKeySize {
			return errors.New("netsec: invalid Ed25519 key")
		}
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedAlgorithm, public)
	}
	return nil
}
