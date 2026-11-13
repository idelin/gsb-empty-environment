package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"
)

var (
	// ErrInvalidIdentityName is returned when an identity name is empty or is
	// not valid UTF-8.
	ErrInvalidIdentityName = errors.New("netsec: invalid identity name")
	// ErrInvalidPublicKey is returned for an Ed25519 public key with the wrong
	// length or a private key that does not belong to the registered identity.
	ErrInvalidPublicKey = errors.New("netsec: invalid Ed25519 public key")
	// ErrIdentityNotFound is returned when a host does not know an identity.
	ErrIdentityNotFound = errors.New("netsec: identity not found")
	// ErrIdentityConflict is returned when a name or public key is registered
	// more than once. A host registry is fixed and immutable for a session.
	ErrIdentityConflict = errors.New("netsec: identity registry conflict")
	// ErrInvalidPrivateKey is returned for a malformed Ed25519 private key.
	ErrInvalidPrivateKey = errors.New("netsec: invalid Ed25519 private key")
)

// Identity is a host-fixed binding between a non-empty UTF-8 name and an
// Ed25519 public key. PublicKey is a copy owned by the value.
type Identity struct {
	Name      string
	PublicKey ed25519.PublicKey
}

func cloneIdentity(id Identity) Identity {
	return Identity{Name: id.Name, PublicKey: append(ed25519.PublicKey(nil), id.PublicKey...)}
}

// Registry is a host-owned identity registry. It maps each name to exactly one
// Ed25519 public key and each public key to exactly one name. Handshake messages
// never contribute public keys; they only carry names that are resolved here.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]ed25519.PublicKey
	byKey  map[string]string
}

// NewRegistry returns an empty host identity registry.
func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]ed25519.PublicKey),
		byKey:  make(map[string]string),
	}
}

// NewInMemoryRegistry is an alias for NewRegistry.
func NewInMemoryRegistry() *Registry { return NewRegistry() }

// Register adds a name/public-key binding. Registration fails if the name is
// empty, is not UTF-8, the key has the wrong size, or either side is already
// bound. Re-registering the same pair is also rejected because a host's
// registry must not silently change during a session.
func (r *Registry) Register(name string, publicKey ed25519.PublicKey) error {
	if r == nil {
		return errors.New("netsec: nil registry")
	}
	if name == "" || !utf8.ValidString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidIdentityName, name)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: length %d", ErrInvalidPublicKey, len(publicKey))
	}
	key := append(ed25519.PublicKey(nil), publicKey...)
	keyID := string(key)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byName[name]; ok {
		return fmt.Errorf("%w: name %q already registered", ErrIdentityConflict, name)
	}
	if other, ok := r.byKey[keyID]; ok {
		return fmt.Errorf("%w: public key already registered as %q", ErrIdentityConflict, other)
	}
	r.byName[name] = key
	r.byKey[keyID] = name
	return nil
}

// RegisterIdentity registers an Identity value.
func (r *Registry) RegisterIdentity(id Identity) error {
	return r.Register(id.Name, id.PublicKey)
}

// Lookup resolves a name through the host registry.
func (r *Registry) Lookup(name string) (Identity, error) {
	if r == nil {
		return Identity{}, errors.New("netsec: nil registry")
	}
	r.mu.RLock()
	key, ok := r.byName[name]
	r.mu.RUnlock()
	if !ok {
		return Identity{}, fmt.Errorf("%w: %q", ErrIdentityNotFound, name)
	}
	return Identity{Name: name, PublicKey: append(ed25519.PublicKey(nil), key...)}, nil
}

// LookupPublicKey resolves a name to a copy of its registered public key.
func (r *Registry) LookupPublicKey(name string) (ed25519.PublicKey, error) {
	id, err := r.Lookup(name)
	if err != nil {
		return nil, err
	}
	return id.PublicKey, nil
}

// NameFor returns the unique registered name for a public key.
func (r *Registry) NameFor(publicKey ed25519.PublicKey) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.RLock()
	name, ok := r.byKey[string(publicKey)]
	r.mu.RUnlock()
	return name, ok
}

// Len reports the number of registered identities.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}

// Party is one host endpoint: a registered identity, its Ed25519 private key,
// the host's fixed registry, and the host's session-ID replay table.
type Party struct {
	ID         Identity
	PrivateKey ed25519.PrivateKey
	Registry   *Registry
	Sessions   *SessionTable
}

// NewParty creates a local endpoint and verifies that its private key matches
// the host's registered public key. The registry is authoritative.
func NewParty(name string, privateKey ed25519.PrivateKey, registry *Registry) (*Party, error) {
	if registry == nil {
		return nil, errors.New("netsec: nil registry")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: length %d", ErrInvalidPrivateKey, len(privateKey))
	}
	id, err := registry.Lookup(name)
	if err != nil {
		return nil, err
	}
	derived := privateKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(derived, id.PublicKey) {
		return nil, fmt.Errorf("%w: private key does not match registered identity %q", ErrInvalidPrivateKey, name)
	}
	return &Party{
		ID:         cloneIdentity(id),
		PrivateKey: append(ed25519.PrivateKey(nil), privateKey...),
		Registry:   registry,
		Sessions:   NewSessionTable(),
	}, nil
}

// GenerateParty generates an Ed25519 key pair, registers the public key under
// name, and returns the local endpoint.
func GenerateParty(name string, registry *Registry, random io.Reader) (*Party, error) {
	if registry == nil {
		return nil, errors.New("netsec: nil registry")
	}
	if random == nil {
		random = defaultRand
	}
	publicKey, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, err
	}
	if err := registry.Register(name, publicKey); err != nil {
		return nil, err
	}
	return NewParty(name, privateKey, registry)
}

func (p *Party) sign(message []byte) []byte {
	return ed25519.Sign(p.PrivateKey, message)
}

func (p *Party) publicIdentity() Identity { return cloneIdentity(p.ID) }
