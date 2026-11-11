package pwupgrade

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Stored hash formats:
//
//	legacy:   sha1$<salt-base64>$<sha1(salt+password)-base64>
//	current:  pbkdf2_sha256$<iterations>$<salt-base64>$<key-base64>
//
// Base64 uses the unpadded standard alphabet (RawStdEncoding).
const (
	algSHA1   = "sha1"
	algPBKDF2 = "pbkdf2_sha256"

	currentIterations = 600_000 // OWASP PBKDF2-HMAC-SHA256 guidance
	currentSaltLen    = 16
	currentKeyLen     = 32

	// Bounds that keep untrusted stored hashes from causing excessive work
	// or oversized allocations.
	maxIterations = 10_000_000
	maxSaltLen    = 1024
	maxKeyLen     = 1024
)

var (
	// ErrUnknownFormat is returned when the hash uses an unrecognized algorithm tag.
	ErrUnknownFormat = errors.New("pwupgrade: unknown hash format")
	// ErrMalformedHash is returned when a recognized hash is structurally invalid.
	ErrMalformedHash = errors.New("pwupgrade: malformed hash")
	// ErrInvalidParameters is returned when iterations or lengths are out of bounds.
	ErrInvalidParameters = errors.New("pwupgrade: invalid hash parameters")
	// ErrNotFound is returned when the user does not exist in the store.
	ErrNotFound = errors.New("pwupgrade: user not found")
)

var b64 = base64.RawStdEncoding

type parsedHash struct {
	algorithm  string
	iterations int // PBKDF2 only
	salt       []byte
	key        []byte
}

// Hash produces a fresh PBKDF2-SHA256 hash of password using current parameters.
func Hash(password string) (string, error) {
	salt := make([]byte, currentSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, currentIterations, currentKeyLen)
	if err != nil {
		return "", err
	}
	return encodePBKDF2(currentIterations, salt, key), nil
}

// Verify checks password against storedHash in constant time relative to the
// digest length. A structurally invalid stored hash returns an error; a wrong
// password returns (false, nil).
func Verify(password, storedHash string) (bool, error) {
	parsed, err := parse(storedHash)
	if err != nil {
		return false, err
	}
	got, err := derive(password, parsed)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, parsed.key) == 1, nil
}

// NeedsUpgrade reports whether a verified hash predates the current parameters
// and should be rehashed after a successful login.
func NeedsUpgrade(storedHash string) (bool, error) {
	parsed, err := parse(storedHash)
	if err != nil {
		return false, err
	}
	if parsed.algorithm == algSHA1 {
		return true, nil
	}
	return parsed.iterations < currentIterations || len(parsed.key) != currentKeyLen, nil
}

// UserStore persists password hashes. Implementations must be safe for
// concurrent use.
type UserStore interface {
	// GetHash returns the stored hash for the user, or ErrNotFound.
	GetHash(userID string) (string, error)
	// CompareAndSwapHash writes newHash only if the stored value still equals
	// oldHash. It returns (false, nil) when the comparison failed, meaning the
	// password was changed concurrently and the upgrade must be abandoned.
	CompareAndSwapHash(userID, oldHash, newHash string) (bool, error)
	// SetPassword replaces the hash unconditionally (password change flow).
	SetPassword(userID, newHash string) error
}

// VerifyAndUpgrade is the login entry point. It verifies the password and, on
// success, transparently rehashes legacy or weak hashes with current
// parameters. The write is compare-and-swap: if the user changed their
// password between verification and the write, the upgrade is abandoned
// without overwriting the newer hash.
func VerifyAndUpgrade(store UserStore, userID, password string) (bool, error) {
	stored, err := store.GetHash(userID)
	if err != nil {
		return false, err
	}

	ok, err := Verify(password, stored)
	if err != nil || !ok {
		return ok, err
	}

	upgrade, err := NeedsUpgrade(stored)
	if err != nil || !upgrade {
		return true, err
	}

	newHash, err := Hash(password)
	if err != nil {
		// Verification succeeded; a rehash failure must not fail the login.
		return true, nil
	}

	swapped, err := store.CompareAndSwapHash(userID, stored, newHash)
	if err != nil {
		return true, nil
	}
	if !swapped {
		// Concurrent password change: leave the newer hash untouched.
		return true, nil
	}
	return true, nil
}

func derive(password string, parsed *parsedHash) ([]byte, error) {
	switch parsed.algorithm {
	case algSHA1:
		sum := sha1.Sum(append(append([]byte(nil), parsed.salt...), []byte(password)...))
		return sum[:], nil
	case algPBKDF2:
		return pbkdf2.Key(sha256.New, password, parsed.salt, parsed.iterations, len(parsed.key))
	default:
		return nil, ErrUnknownFormat
	}
}

func parse(stored string) (*parsedHash, error) {
	parts := strings.Split(stored, "$")
	if len(parts) < 2 {
		return nil, ErrMalformedHash
	}
	switch parts[0] {
	case algSHA1:
		if len(parts) != 3 {
			return nil, ErrMalformedHash
		}
		salt, err := decodeField(parts[1])
		if err != nil {
			return nil, fmt.Errorf("%w: salt: %v", ErrMalformedHash, err)
		}
		key, err := decodeField(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: key: %v", ErrMalformedHash, err)
		}
		if len(salt) == 0 || len(salt) > maxSaltLen || len(key) != sha1.Size {
			return nil, ErrInvalidParameters
		}
		return &parsedHash{algorithm: algSHA1, salt: salt, key: key}, nil
	case algPBKDF2:
		if len(parts) != 4 {
			return nil, ErrMalformedHash
		}
		iterations, err := strconv.Atoi(parts[1])
		if err != nil || iterations <= 0 || iterations > maxIterations {
			return nil, fmt.Errorf("%w: iterations", ErrInvalidParameters)
		}
		salt, err := decodeField(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: salt: %v", ErrMalformedHash, err)
		}
		key, err := decodeField(parts[3])
		if err != nil {
			return nil, fmt.Errorf("%w: key: %v", ErrMalformedHash, err)
		}
		if len(salt) == 0 || len(salt) > maxSaltLen || len(key) == 0 || len(key) > maxKeyLen {
			return nil, ErrInvalidParameters
		}
		return &parsedHash{
			algorithm:  algPBKDF2,
			iterations: iterations,
			salt:       salt,
			key:        key,
		}, nil
	default:
		return nil, ErrUnknownFormat
	}
}

func decodeField(field string) ([]byte, error) {
	if field == "" {
		return nil, errors.New("empty field")
	}
	return b64.DecodeString(field)
}

func encodePBKDF2(iterations int, salt, key []byte) string {
	return strings.Join([]string{
		algPBKDF2,
		strconv.Itoa(iterations),
		b64.EncodeToString(salt),
		b64.EncodeToString(key),
	}, "$")
}

// EncodeLegacySHA1 builds a legacy sha1$salt$hash value. It is exported for
// tests and one-time data migration tooling.
func EncodeLegacySHA1(salt []byte, password string) string {
	sum := sha1.Sum(append(append([]byte(nil), salt...), []byte(password)...))
	return strings.Join([]string{algSHA1, b64.EncodeToString(salt), b64.EncodeToString(sum[:])}, "$")
}

// MemoryStore is an in-memory UserStore safe for concurrent use.
type MemoryStore struct {
	users map[string]string
	mu    sync.RWMutex

	// BeforeCAS, if non-nil, is invoked inside CompareAndSwapHash after the
	// current hash has been read but before the compare-and-swap write lock is
	// taken. Tests use it to interleave a concurrent password change.
	BeforeCAS func()
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: make(map[string]string)}
}

// Put inserts or replaces a user hash.
func (s *MemoryStore) Put(userID, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[userID] = hash
}

// GetHash implements UserStore.
func (s *MemoryStore) GetHash(userID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hash, ok := s.users[userID]
	if !ok {
		return "", ErrNotFound
	}
	return hash, nil
}

// SetPassword implements UserStore.
func (s *MemoryStore) SetPassword(userID, newHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return ErrNotFound
	}
	s.users[userID] = newHash
	return nil
}

// CompareAndSwapHash implements UserStore.
func (s *MemoryStore) CompareAndSwapHash(userID, oldHash, newHash string) (bool, error) {
	s.mu.RLock()
	current, ok := s.users[userID]
	s.mu.RUnlock()
	if !ok {
		return false, ErrNotFound
	}
	if s.BeforeCAS != nil {
		s.BeforeCAS()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok = s.users[userID]
	if !ok {
		return false, ErrNotFound
	}
	if subtle.ConstantTimeCompare([]byte(current), []byte(oldHash)) != 1 {
		return false, nil
	}
	s.users[userID] = newHash
	return true, nil
}
