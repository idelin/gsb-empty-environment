package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
)

// RecoveryMethod names one independently verified recovery factor, such as an
// emailed one-time code or a TOTP code.
type RecoveryMethod string

const (
	RecoveryMethodEmail        RecoveryMethod = "email"
	RecoveryMethodTOTP         RecoveryMethod = "totp"
	RecoveryMethodRecoveryCode RecoveryMethod = "recovery_code"
	RecoveryMethodPasskey      RecoveryMethod = "passkey"
)

// PolicyDigest identifies a recovery policy. Accounts store only the digest;
// the trusted host stores the policy itself.
type PolicyDigest [32]byte

func (d PolicyDigest) String() string { return hex.EncodeToString(d[:]) }

// RecoveryPolicy is a conjunction of required methods. After canonicalization
// MinDistinct must equal the number of distinct methods; an inconsistent value
// is rejected rather than silently weakening the policy.
type RecoveryPolicy struct {
	Required    []RecoveryMethod
	MinDistinct int
}

// ErrInvalidPolicy is returned for an empty or internally inconsistent policy.
var ErrInvalidPolicy = errors.New("netsec: invalid recovery policy")

// Canonical returns a copy with sorted, de-duplicated, non-empty methods and a
// normalized MinDistinct.
func (p RecoveryPolicy) Canonical() (RecoveryPolicy, error) {
	methods := canonicalMethods(p.Required)
	if len(methods) == 0 {
		return RecoveryPolicy{}, ErrInvalidPolicy
	}
	minDistinct := p.MinDistinct
	if minDistinct == 0 {
		minDistinct = len(methods)
	}
	if minDistinct != len(methods) {
		return RecoveryPolicy{}, ErrInvalidPolicy
	}
	return RecoveryPolicy{
		Required:    methods,
		MinDistinct: minDistinct,
	}, nil
}

// Digest returns the canonical policy digest.
func (p RecoveryPolicy) Digest() (PolicyDigest, error) {
	canonical, err := p.Canonical()
	if err != nil {
		return PolicyDigest{}, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("netsec/recovery-policy/v1\x00"))
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(canonical.Required)))
	_, _ = h.Write(count[:])
	for _, method := range canonical.Required {
		writeString(h, string(method))
	}
	var minimum [8]byte
	binary.BigEndian.PutUint64(minimum[:], uint64(canonical.MinDistinct))
	_, _ = h.Write(minimum[:])
	var digest PolicyDigest
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

// SatisfiedBy reports whether the verified method set satisfies the policy.
// Duplicate factors never count twice.
func (p RecoveryPolicy) SatisfiedBy(methods []RecoveryMethod) bool {
	canonical, err := p.Canonical()
	if err != nil {
		return false
	}
	have := make(map[RecoveryMethod]struct{}, len(methods))
	for _, method := range methods {
		if method != "" {
			have[method] = struct{}{}
		}
	}
	for _, required := range canonical.Required {
		if _, ok := have[required]; !ok {
			return false
		}
	}
	return len(have) >= canonical.MinDistinct
}

func canonicalMethods(methods []RecoveryMethod) []RecoveryMethod {
	set := make(map[RecoveryMethod]struct{}, len(methods))
	for _, method := range methods {
		if method != "" {
			set[method] = struct{}{}
		}
	}
	out := make([]RecoveryMethod, 0, len(set))
	for method := range set {
		out = append(out, method)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// VerifiedRecovery is returned by a caller-supplied verifier. Methods are the
// factors the verifier actually checked. EvidenceDigest binds the original
// proof bytes; if zero, the service hashes the proof bytes itself.
type VerifiedRecovery struct {
	Methods        []RecoveryMethod
	EvidenceDigest [32]byte
}

// RecoveryVerifier verifies the out-of-band recovery proof. It must not trust
// an account's policy: policy evaluation happens inside the atomic commit
// against the account's *current* policy digest.
type RecoveryVerifier func(ctx context.Context, accountID AccountID, proof []byte) (VerifiedRecovery, error)

// ErrRecoveryProofRejected indicates that the verifier rejected the proof.
var ErrRecoveryProofRejected = errors.New("netsec: recovery proof rejected")

func proofDigest(accountID AccountID, methods []RecoveryMethod, evidence [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("netsec/recovery-proof/v1\x00"))
	writeString(h, string(accountID))
	for _, method := range canonicalMethods(methods) {
		writeString(h, string(method))
	}
	_, _ = h.Write(evidence[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeString(w interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = w.Write(length[:])
	_, _ = w.Write([]byte(value))
}
