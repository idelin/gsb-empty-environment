package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// PBKDF2SHA256 is the algorithm identifier of the standard-library hasher.
const PBKDF2SHA256 = "pbkdf2-sha256"

// Upper bounds keep caller-supplied work factors from becoming a memory or CPU
// denial-of-service primitive. They are far above any normal password policy.
const (
	MaxPasswordIterations = 10_000_000
	MaxPasswordSaltBytes  = 4_096
	MaxPasswordKeyBytes   = 4_096
)

// PasswordParams are bound into a reset transaction and into the stored hash.
// They are deliberately explicit so a reset cannot silently downgrade cost.
type PasswordParams struct {
	Algorithm  string
	Iterations uint32
	SaltBytes  uint32
	KeyBytes   uint32
}

// PasswordPolicy is the minimum accepted work factor and password length.
type PasswordPolicy struct {
	MinIterations    uint32
	MinSaltBytes     uint32
	MinKeyBytes      uint32
	MinPasswordBytes int
}

// DefaultPasswordPolicy is a conservative PBKDF2-SHA256 floor.
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{
		MinIterations:    210_000,
		MinSaltBytes:     16,
		MinKeyBytes:      32,
		MinPasswordBytes: 12,
	}
}

// DefaultPasswordParams derives work factors that meet or exceed policy.
func DefaultPasswordParams(policy PasswordPolicy) PasswordParams {
	iterations := policy.MinIterations
	if iterations == 0 {
		iterations = 1
	}
	saltBytes := policy.MinSaltBytes
	if saltBytes < 16 {
		saltBytes = 16
	}
	keyBytes := policy.MinKeyBytes
	if keyBytes < 32 {
		keyBytes = 32
	}
	return PasswordParams{
		Algorithm:  PBKDF2SHA256,
		Iterations: iterations,
		SaltBytes:  saltBytes,
		KeyBytes:   keyBytes,
	}
}

// Equal reports exact parameter equality.
func (p PasswordParams) Equal(other PasswordParams) bool { return p == other }

// Validate enforces the policy floor.
func (p PasswordParams) Validate(policy PasswordPolicy) error {
	if p.Algorithm == "" {
		return fmt.Errorf("%w: empty password algorithm", ErrWeakPasswordParams)
	}
	if p.Iterations == 0 || p.Iterations < policy.MinIterations {
		return fmt.Errorf("%w: iterations %d < %d", ErrWeakPasswordParams, p.Iterations, policy.MinIterations)
	}
	if p.Iterations > MaxPasswordIterations {
		return fmt.Errorf("%w: iterations %d > %d", ErrWeakPasswordParams, p.Iterations, MaxPasswordIterations)
	}
	if p.SaltBytes == 0 || p.SaltBytes < policy.MinSaltBytes {
		return fmt.Errorf("%w: salt bytes %d < %d", ErrWeakPasswordParams, p.SaltBytes, policy.MinSaltBytes)
	}
	if p.SaltBytes > MaxPasswordSaltBytes {
		return fmt.Errorf("%w: salt bytes %d > %d", ErrWeakPasswordParams, p.SaltBytes, MaxPasswordSaltBytes)
	}
	if p.KeyBytes == 0 || p.KeyBytes < policy.MinKeyBytes {
		return fmt.Errorf("%w: key bytes %d < %d", ErrWeakPasswordParams, p.KeyBytes, policy.MinKeyBytes)
	}
	if p.KeyBytes > MaxPasswordKeyBytes {
		return fmt.Errorf("%w: key bytes %d > %d", ErrWeakPasswordParams, p.KeyBytes, MaxPasswordKeyBytes)
	}
	return nil
}

// PasswordHash is a self-describing password hash. Params are part of the
// value, so verification never has to guess the work factor.
type PasswordHash struct {
	Algorithm string
	Params    PasswordParams
	Salt      []byte
	Digest    []byte
}

// Clone makes a deep copy suitable for storing in an account.
func (h PasswordHash) Clone() PasswordHash {
	h.Salt = append([]byte(nil), h.Salt...)
	h.Digest = append([]byte(nil), h.Digest...)
	return h
}

// Valid checks structural invariants and the policy floor.
func (h PasswordHash) Valid(policy PasswordPolicy) error {
	if h.Algorithm == "" || h.Params.Algorithm != h.Algorithm {
		return fmt.Errorf("%w: hash algorithm mismatch", ErrWeakPasswordParams)
	}
	if err := h.Params.Validate(policy); err != nil {
		return err
	}
	if uint32(len(h.Salt)) != h.Params.SaltBytes {
		return fmt.Errorf("%w: stored salt length mismatch", ErrWeakPasswordParams)
	}
	if uint32(len(h.Digest)) != h.Params.KeyBytes {
		return fmt.Errorf("%w: stored key length mismatch", ErrWeakPasswordParams)
	}
	return nil
}

var (
	// ErrWeakPasswordParams indicates a missing work-factor floor.
	ErrWeakPasswordParams = errors.New("netsec: weak password parameters")
	// ErrWeakPassword indicates a password below the configured length floor.
	ErrWeakPassword = errors.New("netsec: password too short")
)

// PasswordHasher is deliberately an interface so a host can use Argon2id or a
// hardware-isolated KMS without changing the reset transaction.
type PasswordHasher interface {
	Hash(ctx context.Context, password []byte, params PasswordParams) (PasswordHash, error)
	Verify(ctx context.Context, password []byte, hash PasswordHash) (bool, error)
}

// PBKDF2Hasher is a standard-library PBKDF2-HMAC-SHA256 implementation.
type PBKDF2Hasher struct{}

func (PBKDF2Hasher) Hash(ctx context.Context, password []byte, params PasswordParams) (PasswordHash, error) {
	if params.Algorithm == "" {
		params.Algorithm = PBKDF2SHA256
	}
	if params.Algorithm != PBKDF2SHA256 {
		return PasswordHash{}, fmt.Errorf("netsec: unsupported password algorithm %q", params.Algorithm)
	}
	if params.Iterations == 0 || params.Iterations > MaxPasswordIterations ||
		params.SaltBytes == 0 || params.SaltBytes > MaxPasswordSaltBytes ||
		params.KeyBytes == 0 || params.KeyBytes > MaxPasswordKeyBytes {
		return PasswordHash{}, ErrWeakPasswordParams
	}
	salt := make([]byte, params.SaltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return PasswordHash{}, err
	}
	digest, err := pbkdf2SHA256(ctx, password, salt, int(params.Iterations), int(params.KeyBytes))
	if err != nil {
		return PasswordHash{}, err
	}
	return PasswordHash{
		Algorithm: params.Algorithm,
		Params:    params,
		Salt:      salt,
		Digest:    digest,
	}, nil
}

func (PBKDF2Hasher) Verify(ctx context.Context, password []byte, hash PasswordHash) (bool, error) {
	if hash.Algorithm != PBKDF2SHA256 || hash.Params.Algorithm != PBKDF2SHA256 {
		return false, fmt.Errorf("netsec: unsupported password algorithm %q", hash.Algorithm)
	}
	if hash.Params.Iterations == 0 || hash.Params.Iterations > MaxPasswordIterations ||
		hash.Params.SaltBytes == 0 || hash.Params.SaltBytes > MaxPasswordSaltBytes ||
		hash.Params.KeyBytes == 0 || hash.Params.KeyBytes > MaxPasswordKeyBytes ||
		uint32(len(hash.Salt)) != hash.Params.SaltBytes ||
		uint32(len(hash.Digest)) != hash.Params.KeyBytes {
		return false, ErrWeakPasswordParams
	}
	digest, err := pbkdf2SHA256(ctx, password, hash.Salt, int(hash.Params.Iterations), len(hash.Digest))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(digest, hash.Digest) == 1, nil
}

// PreparedPassword is produced outside the store transaction. Its fields are
// unexported so callers cannot fabricate an unchecked hash artifact.
type PreparedPassword struct {
	hash        PasswordHash
	params      PasswordParams
	fingerprint [32]byte
}

// Params returns the exact work factors bound to this artifact.
func (p *PreparedPassword) Params() PasswordParams {
	if p == nil {
		return PasswordParams{}
	}
	return p.params
}

// Fingerprint is a keyed digest used only to recognise an idempotent retry of
// the same target password. It is not stored in the account record.
func (p *PreparedPassword) Fingerprint() [32]byte {
	if p == nil {
		return [32]byte{}
	}
	return p.fingerprint
}

func (p *PreparedPassword) valid(policy PasswordPolicy) error {
	if p == nil {
		return errors.New("netsec: nil prepared password")
	}
	if err := p.params.Validate(policy); err != nil {
		return err
	}
	if !p.params.Equal(p.hash.Params) {
		return errors.New("netsec: prepared password parameter mismatch")
	}
	return p.hash.Valid(policy)
}

func passwordFingerprint(key []byte, password []byte) [32]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("netsec/password-fingerprint/v1\x00"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(password)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write(password)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func pbkdf2SHA256(ctx context.Context, password, salt []byte, iterations, keyLen int) ([]byte, error) {
	if iterations <= 0 || keyLen <= 0 {
		return nil, ErrWeakPasswordParams
	}
	const hashLen = sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	var counter [4]byte
	for block := 1; block <= blocks; block++ {
		if block&7 == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write(counter[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			if i&1023 == 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
			}
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen], nil
}
