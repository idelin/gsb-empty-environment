package netsec

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Sentinel errors reported by the store. Callers should use errors.Is.
var (
	// ErrNotFound means the requested object, pointer or blob does not exist.
	ErrNotFound = errors.New("netsec: not found")
	// ErrConflict means a concurrent update moved the pointer first, or a blob
	// with the same key but different content already exists.
	ErrConflict = errors.New("netsec: concurrent update conflict")
	// ErrTampered means an envelope failed authentication, its digest did not
	// match the pointer, or its authenticated metadata was inconsistent.
	ErrTampered = errors.New("netsec: authenticated data mismatch")
	// ErrInjected is returned when a configured fault aborts an operation.
	ErrInjected = errors.New("netsec: injected fault")
	// ErrInvalidObjectID is returned for empty or oversized object identifiers.
	ErrInvalidObjectID = errors.New("netsec: invalid object id")
)

const (
	magic         = "AEV1"
	maxObjectID   = 4096
	maxCASRetries = 1000
)

// Pointer is the current-version metadata for an object. It is the trusted
// root of the version chain: blobs are content-addressed by Digest, so moving
// the pointer is the only way to make a version current.
type Pointer struct {
	Version uint64
	Digest  []byte
}

// BlobStore stores sealed envelopes keyed by (objectID, envelopeDigest).
// Implementations must make Put atomic and idempotent: storing the same
// digest twice with identical content succeeds, different content conflicts.
type BlobStore interface {
	Put(ctx context.Context, objectID string, digest, envelope []byte) error
	Get(ctx context.Context, objectID string, digest []byte) ([]byte, error)
	Has(ctx context.Context, objectID string, digest []byte) (bool, error)
}

// MetadataStore holds the current pointer per object and moves it with
// compare-and-swap semantics. old == nil means the object is expected to be
// new; a non-nil old must exactly match the stored pointer. Implementations
// must reject any attempt to move the pointer backwards.
type MetadataStore interface {
	Get(ctx context.Context, objectID string) (*Pointer, bool, error)
	CAS(ctx context.Context, objectID string, old, new *Pointer) error
}

// Store is an authenticated, versioned encryption wrapper around a BlobStore
// and a MetadataStore. Every envelope authenticates the object id, version,
// previous version digest and plaintext length as AEAD additional data, so a
// legitimate old ciphertext cannot be replayed as a newer or different object.
type Store struct {
	blob      BlobStore
	meta      MetadataStore
	aead      cipher.AEAD
	nonceSize int
}

// GenerateKey returns a fresh 32-byte AES-256 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("netsec: generate key: %w", err)
	}
	return key, nil
}

// New builds a Store. key must be 16, 24 or 32 bytes for AES-128/192/256-GCM.
func New(blob BlobStore, meta MetadataStore, key []byte) (*Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("netsec: cipher init: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: gcm init: %w", err)
	}
	return &Store{blob: blob, meta: meta, aead: aead, nonceSize: aead.NonceSize()}, nil
}

func validateObjectID(objectID string) error {
	if objectID == "" || len(objectID) > maxObjectID {
		return ErrInvalidObjectID
	}
	return nil
}

// encodeAAD serializes the fields authenticated (but not encrypted) with every
// envelope: object id, version, previous-version envelope digest, and the
// plaintext length. The magic prefix domain-separates these AADs.
func encodeAAD(objectID string, version uint64, prevDigest []byte, plainLen uint64) []byte {
	b := make([]byte, 0, len(magic)+2+len(objectID)+8+2+len(prevDigest)+8)
	b = append(b, magic...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(objectID)))
	b = append(b, objectID...)
	b = binary.BigEndian.AppendUint64(b, version)
	b = binary.BigEndian.AppendUint16(b, uint16(len(prevDigest)))
	b = append(b, prevDigest...)
	b = binary.BigEndian.AppendUint64(b, plainLen)
	return b
}

// seal produces an on-disk envelope and its sha256 digest.
//
// Layout (all integers big-endian):
//
//	"AEV1" | nonceLen u16 | nonce | idLen u16 | objectID |
//	version u64 | prevLen u16 | prevDigest | plainLen u64 |
//	GCM(ciphertext || tag)
//
// The GCM AAD binds objectID, version, prevDigest and plainLen.
func (s *Store) seal(objectID string, version uint64, prevDigest, plaintext []byte) ([]byte, []byte) {
	nonce := make([]byte, s.nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		panic(fmt.Errorf("netsec: nonce generation: %w", err))
	}

	header := make([]byte, 0, len(magic)+2+len(nonce)+2+len(objectID)+8+2+len(prevDigest)+8)
	header = append(header, magic...)
	header = binary.BigEndian.AppendUint16(header, uint16(len(nonce)))
	header = append(header, nonce...)
	header = binary.BigEndian.AppendUint16(header, uint16(len(objectID)))
	header = append(header, objectID...)
	header = binary.BigEndian.AppendUint64(header, version)
	header = binary.BigEndian.AppendUint16(header, uint16(len(prevDigest)))
	header = append(header, prevDigest...)
	header = binary.BigEndian.AppendUint64(header, uint64(len(plaintext)))

	aad := encodeAAD(objectID, version, prevDigest, uint64(len(plaintext)))
	sealed := s.aead.Seal(header, nonce, plaintext, aad)
	digest := sha256.Sum256(sealed)
	return sealed, digest[:]
}

type envelope struct {
	nonce      []byte
	objectID   string
	version    uint64
	prevDigest []byte
	plainLen   uint64
	ciphertext []byte
}

func parseEnvelope(raw []byte) (*envelope, error) {
	b := raw
	take := func(n int) ([]byte, bool) {
		if len(b) < n {
			return nil, false
		}
		out := b[:n]
		b = b[n:]
		return out, true
	}
	takeU16 := func() (uint16, bool) {
		v, ok := take(2)
		if !ok {
			return 0, false
		}
		return binary.BigEndian.Uint16(v), true
	}
	takeU64 := func() (uint64, bool) {
		v, ok := take(8)
		if !ok {
			return 0, false
		}
		return binary.BigEndian.Uint64(v), true
	}

	m, ok := take(len(magic))
	if !ok || string(m) != magic {
		return nil, ErrTampered
	}
	nonceLen, ok := takeU16()
	if !ok || nonceLen == 0 {
		return nil, ErrTampered
	}
	nonce, ok := take(int(nonceLen))
	if !ok {
		return nil, ErrTampered
	}
	idLen, ok := takeU16()
	if !ok {
		return nil, ErrTampered
	}
	id, ok := take(int(idLen))
	if !ok {
		return nil, ErrTampered
	}
	version, ok := takeU64()
	if !ok {
		return nil, ErrTampered
	}
	prevLen, ok := takeU16()
	if !ok {
		return nil, ErrTampered
	}
	prev, ok := take(int(prevLen))
	if !ok {
		return nil, ErrTampered
	}
	plainLen, ok := takeU64()
	if !ok {
		return nil, ErrTampered
	}
	if len(b) == 0 {
		return nil, ErrTampered
	}
	return &envelope{
		nonce:      nonce,
		objectID:   string(id),
		version:    version,
		prevDigest: prev,
		plainLen:   plainLen,
		ciphertext: b,
	}, nil
}

// openAt authenticates and decrypts the envelope addressed by digest and
// asserts every AAD field against the expected chain position.
func (s *Store) openAt(ctx context.Context, objectID string, wantVersion uint64, digest []byte) (*envelope, []byte, error) {
	raw, err := s.blob.Get(ctx, objectID, digest)
	if err != nil {
		return nil, nil, err
	}
	actual := sha256.Sum256(raw)
	if !equalDigest(actual[:], digest) {
		return nil, nil, fmt.Errorf("%w: envelope digest does not match pointer", ErrTampered)
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, nil, err
	}
	if env.objectID != objectID || env.version != wantVersion {
		return nil, nil, fmt.Errorf("%w: envelope object/version binding mismatch", ErrTampered)
	}
	if len(env.nonce) != s.nonceSize {
		return nil, nil, fmt.Errorf("%w: nonce size mismatch", ErrTampered)
	}
	aad := encodeAAD(env.objectID, env.version, env.prevDigest, env.plainLen)
	plain, err := s.aead.Open(nil, env.nonce, env.ciphertext, aad)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: AEAD authentication failed: %v", ErrTampered, err)
	}
	if uint64(len(plain)) != env.plainLen {
		return nil, nil, fmt.Errorf("%w: plaintext length mismatch", ErrTampered)
	}
	return env, plain, nil
}

// Write encrypts plaintext and appends a new version. The pointer only moves
// when the stored pointer still matches the previous version this write was
// built on; otherwise the write is rebuilt and retried.
func (s *Store) Write(ctx context.Context, objectID string, plaintext []byte) (*Pointer, error) {
	if err := validateObjectID(objectID); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cur, exists, err := s.meta.Get(ctx, objectID)
		if err != nil {
			return nil, err
		}

		var version uint64 = 1
		var prevDigest []byte
		if exists {
			version = cur.Version + 1
			prevDigest = cur.Digest
		}

		envelopeBytes, digest := s.seal(objectID, version, prevDigest, plaintext)
		if err := s.blob.Put(ctx, objectID, digest, envelopeBytes); err != nil {
			return nil, err
		}
		next := &Pointer{Version: version, Digest: digest}
		if err := s.meta.CAS(ctx, objectID, cur, next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return nil, err
		}
		return next, nil
	}
	return nil, fmt.Errorf("%w: exceeded %d retries", ErrConflict, maxCASRetries)
}

// Current returns the current pointer without decrypting anything.
func (s *Store) Current(ctx context.Context, objectID string) (*Pointer, error) {
	if err := validateObjectID(objectID); err != nil {
		return nil, err
	}
	cur, exists, err := s.meta.Get(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return cur, nil
}

// Read authenticates and decrypts the current version of an object.
func (s *Store) Read(ctx context.Context, objectID string) ([]byte, *Pointer, error) {
	cur, err := s.Current(ctx, objectID)
	if err != nil {
		return nil, nil, err
	}
	_, plain, err := s.openAt(ctx, objectID, cur.Version, cur.Digest)
	if err != nil {
		return nil, nil, err
	}
	return plain, cur, nil
}

// VerifyChain walks every version from the current pointer back to genesis,
// re-checking each envelope digest, AAD binding and predecessor link.
func (s *Store) VerifyChain(ctx context.Context, objectID string) error {
	if err := validateObjectID(objectID); err != nil {
		return err
	}
	cur, exists, err := s.meta.Get(ctx, objectID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	// Walk the chain from the tip back to genesis. At each step digest
	// addresses the envelope being opened; after opening it we confirm that
	// the child envelope (already walked) recorded our digest as its parent.
	digest := cur.Digest
	var child *envelope
	for version := cur.Version; version > 0; version-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		env, _, err := s.openAt(ctx, objectID, version, digest)
		if err != nil {
			return err
		}
		if child != nil && !equalDigest(child.prevDigest, digest) {
			return fmt.Errorf("%w: predecessor digest mismatch at version %d", ErrTampered, version)
		}
		if version == 1 {
			if len(env.prevDigest) != 0 {
				return fmt.Errorf("%w: genesis version must have no predecessor", ErrTampered)
			}
			return nil
		}
		child = env
		digest = env.prevDigest
	}
	return fmt.Errorf("%w: chain ended before genesis", ErrTampered)
}

func equalDigest(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
