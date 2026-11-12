package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// DefaultNonceTTL is how long a challenge issued by NonceStore remains valid.
const DefaultNonceTTL = 2 * time.Minute

// NonceStore verifies Ed25519 nonce signatures and consumes each nonce once.
// A nonce is marked consumed only after its signature verifies, so a client may
// retry a failed signature with the same challenge.
type NonceStore struct {
	mu      sync.Mutex
	seen    map[[sha256.Size]byte]struct{}
	pending map[[sha256.Size]byte]time.Time

	// TTL controls the lifetime of nonces returned by Issue or Register.
	TTL time.Duration
	// Now may be replaced before concurrent use, primarily for deterministic
	// tests. A nil value uses time.Now.
	Now func() time.Time
}

// NewNonceStore returns a store with DefaultNonceTTL, or the supplied TTL.
func NewNonceStore(ttl ...time.Duration) *NonceStore {
	effectiveTTL := DefaultNonceTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		effectiveTTL = ttl[0]
	}
	return &NonceStore{
		seen:    make(map[[sha256.Size]byte]struct{}),
		pending: make(map[[sha256.Size]byte]time.Time),
		TTL:     effectiveTTL,
	}
}

// Issue creates a random nonce. The default size is 32 bytes.
func (store *NonceStore) Issue(size ...int) ([]byte, error) {
	n := 32
	if len(size) > 0 {
		n = size[0]
	}
	if n < 16 || n > 4096 {
		return nil, fmt.Errorf("netsec: nonce size must be between 16 and 4096 bytes")
	}
	nonce := make([]byte, n)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("netsec: generate nonce: %w", err)
	}
	if err := store.Register(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

// Register records a server-created challenge so that it must be consumed
// before its TTL expires. Registering an already consumed nonce is a replay.
func (store *NonceStore) Register(nonce []byte) error {
	if len(nonce) == 0 {
		return fmt.Errorf("%w: empty nonce", ErrInvalidNonceSignature)
	}
	if store == nil {
		return fmt.Errorf("netsec: nil nonce store")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.initialise()

	digest := sha256.Sum256(nonce)
	if _, ok := store.seen[digest]; ok {
		return ErrNonceReplay
	}
	if _, ok := store.pending[digest]; ok {
		return nil
	}
	store.pending[digest] = store.clock()
	return nil
}

// Verify verifies a raw Ed25519 signature over nonce with the user's public
// key, then consumes the nonce. A second valid use of the same nonce returns
// ErrNonceReplay.
func (store *NonceStore) Verify(publicKey ed25519.PublicKey, nonce, signature []byte) error {
	if store == nil {
		return fmt.Errorf("netsec: nil nonce store")
	}
	if len(nonce) == 0 {
		return fmt.Errorf("%w: empty nonce", ErrInvalidNonceSignature)
	}
	if !validPublicKey(publicKey) {
		return fmt.Errorf("%w: nonce verifier key", ErrInvalidKey)
	}
	if len(signature) != ed25519.SignatureSize {
		return ErrInvalidNonceSignature
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.initialise()

	digest := sha256.Sum256(nonce)
	if _, ok := store.seen[digest]; ok {
		return ErrNonceReplay
	}
	if issuedAt, ok := store.pending[digest]; ok &&
		store.TTL > 0 && !store.clock().Before(issuedAt.Add(store.TTL)) {
		delete(store.pending, digest)
		return ErrNonceExpired
	}

	if !ed25519.Verify(publicKey, nonce, signature) {
		return ErrInvalidNonceSignature
	}

	store.seen[digest] = struct{}{}
	delete(store.pending, digest)
	return nil
}

// Consume is an alias for Verify.
func (store *NonceStore) Consume(publicKey ed25519.PublicKey, nonce, signature []byte) error {
	return store.Verify(publicKey, nonce, signature)
}

// VerifyNonce is an alias for Verify.
func (store *NonceStore) VerifyNonce(publicKey ed25519.PublicKey, nonce, signature []byte) error {
	return store.Verify(publicKey, nonce, signature)
}

// Reset forgets issued and consumed nonces. It is intended for tests and for a
// deliberate server-side challenge reset.
func (store *NonceStore) Reset() {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.seen = make(map[[sha256.Size]byte]struct{})
	store.pending = make(map[[sha256.Size]byte]time.Time)
}

func (store *NonceStore) initialise() {
	if store.seen == nil {
		store.seen = make(map[[sha256.Size]byte]struct{})
	}
	if store.pending == nil {
		store.pending = make(map[[sha256.Size]byte]time.Time)
	}
	if store.TTL <= 0 {
		store.TTL = DefaultNonceTTL
	}
}

func (store *NonceStore) clock() time.Time {
	if store.Now != nil {
		return store.Now()
	}
	return time.Now()
}
