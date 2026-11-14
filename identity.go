package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"
	"sync"
)

// IdentityProof carries the evidence used to authenticate a data subject.
// SubjectID binds the proof to the subject named in the request. Secret must
// never be logged or persisted by callers.
type IdentityProof struct {
	SubjectID string
	Secret    string
}

// IdentityVerifier authenticates a subject before a request is accepted.
// Implementations must return an error for any failed or ambiguous proof.
type IdentityVerifier interface {
	Verify(ctx context.Context, subject Subject, proof IdentityProof) error
}

var _ IdentityVerifier = (*MemoryIdentityVerifier)(nil)

type identityRecord struct {
	salt [16]byte
	hash [32]byte
}

// MemoryIdentityVerifier is a concurrency-safe, in-memory verifier suitable
// for a single process. It stores salted SHA-256 hashes, not plaintext
// secrets, and returns the same error for unknown subjects and bad secrets.
//
// Production deployments can replace it with a verifier backed by their
// identity provider, passkey, signed assertion or step-up verification flow.
type MemoryIdentityVerifier struct {
	mu      sync.RWMutex
	records map[string]identityRecord
}

// NewMemoryIdentityVerifier creates an empty verifier.
func NewMemoryIdentityVerifier() *MemoryIdentityVerifier {
	return &MemoryIdentityVerifier{records: make(map[string]identityRecord)}
}

// Enroll registers a subject's secret. The subject must not already be
// enrolled. The secret is hashed with a fresh random salt.
func (v *MemoryIdentityVerifier) Enroll(subject Subject, secret string) error {
	if v == nil {
		return fmt.Errorf("%w: nil identity verifier", ErrInvalidConfiguration)
	}
	subject.ID = strings.TrimSpace(subject.ID)
	if subject.ID == "" {
		return invalidRequestf("subject ID is required")
	}
	if secret == "" {
		return invalidRequestf("identity secret is required")
	}

	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return fmt.Errorf("%w: generate identity salt: %v", ErrInvalidConfiguration, err)
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.records[subject.ID]; exists {
		return ErrSubjectAlreadyEnrolled
	}
	v.records[subject.ID] = identityRecord{
		salt: salt,
		hash: hashSecret(salt, secret),
	}
	return nil
}

// Verify implements IdentityVerifier.
func (v *MemoryIdentityVerifier) Verify(_ context.Context, subject Subject, proof IdentityProof) error {
	if v == nil {
		return ErrIdentityVerificationFailed
	}
	subject.ID = strings.TrimSpace(subject.ID)
	if subject.ID == "" || proof.SubjectID != subject.ID || proof.Secret == "" {
		return ErrIdentityVerificationFailed
	}

	v.mu.RLock()
	record, ok := v.records[subject.ID]
	v.mu.RUnlock()
	if !ok {
		return ErrIdentityVerificationFailed
	}

	got := hashSecret(record.salt, proof.Secret)
	if subtle.ConstantTimeCompare(record.hash[:], got[:]) != 1 {
		return ErrIdentityVerificationFailed
	}
	return nil
}

func hashSecret(salt [16]byte, secret string) [32]byte {
	h := sha256.New()
	_, _ = h.Write(salt[:])
	_, _ = h.Write([]byte(secret))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
