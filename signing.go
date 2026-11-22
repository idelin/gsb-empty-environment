package tokenmigrate

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"
	"sort"
	"time"
)

const signatureVersion = "tokenmigrate/sign/v1"

type hashWriter struct {
	h hash.Hash
}

func newHashWriter(parts ...[]byte) *hashWriter {
	h := sha256.New()
	hw := &hashWriter{h: h}
	for _, part := range parts {
		hw.bytes(part)
	}
	return hw
}

func newKeyedHashWriter(key []byte, parts ...[]byte) *hashWriter {
	h := hmac.New(sha256.New, key)
	hw := &hashWriter{h: h}
	for _, part := range parts {
		hw.bytes(part)
	}
	return hw
}

func (w *hashWriter) bytes(b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = w.h.Write(size[:])
	_, _ = w.h.Write(b)
}

func (w *hashWriter) str(s string) {
	w.bytes([]byte(s))
}

func (w *hashWriter) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = w.h.Write(b[:])
}

func (w *hashWriter) timestamp(t time.Time) {
	if t.IsZero() {
		w.str("")
		return
	}
	w.str(t.UTC().Format(time.RFC3339Nano))
}

func (w *hashWriter) period(p Period) {
	w.timestamp(p.NotBefore)
	w.timestamp(p.NotAfter)
}

func (w *hashWriter) sum() []byte {
	return w.h.Sum(nil)
}

func issuanceSigningBytes(r Issuance) []byte {
	w := newHashWriter([]byte(signatureVersion))
	w.str(string(r.ID))
	w.str(string(r.Domain))
	w.u64(uint64(r.Generation))
	w.str(string(r.Token))
	w.str(string(r.Commitment))
	w.period(r.Valid)
	return w.sum()
}

func equalitySigningBytes(p EqualityProof, signer PartyID) []byte {
	left, right := p.Left, p.Right
	if right < left {
		left, right = right, left
	}
	w := newHashWriter([]byte(signatureVersion + "/equality"))
	w.str(p.ID)
	w.str(string(left))
	w.str(string(right))
	w.str(string(signer))
	return w.sum()
}

// SignIssuanceHMAC fills r.Signature using key. The signature covers every
// field of the issuance except Signature itself.
func SignIssuanceHMAC(key []byte, r *Issuance) error {
	if r == nil {
		return fmt.Errorf("%w: nil issuance", ErrInvalidInput)
	}
	if len(key) == 0 {
		return fmt.Errorf("%w: empty HMAC key", ErrInvalidInput)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(issuanceSigningBytes(*r))
	r.Signature = mac.Sum(nil)
	return nil
}

// SignEqualityHMAC returns signer's signature over p.
func SignEqualityHMAC(key []byte, p EqualityProof, signer PartyID) (EqualitySignature, error) {
	if len(key) == 0 {
		return EqualitySignature{}, fmt.Errorf("%w: empty HMAC key", ErrInvalidInput)
	}
	if signer == "" {
		return EqualitySignature{}, fmt.Errorf("%w: empty signer", ErrInvalidInput)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(equalitySigningBytes(p, signer))
	return EqualitySignature{Signer: signer, Signature: mac.Sum(nil)}, nil
}

// HMACVerifier verifies HMAC-SHA256 issuance signatures and two-party
// equality proofs.
type HMACVerifier struct {
	IssuanceKeys map[DomainID][]byte
	EqualityKeys map[PartyID][]byte
}

func (v HMACVerifier) VerifyIssuance(r Issuance) error {
	key, ok := v.IssuanceKeys[r.Domain]
	if !ok || len(key) == 0 {
		return fmt.Errorf("%w: no issuance key for %s", ErrInvalidInput, r.Domain)
	}
	if len(r.Signature) == 0 {
		return fmt.Errorf("%w: issuance %s is unsigned", ErrInvalidInput, r.ID)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(issuanceSigningBytes(r))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(expected, r.Signature) != 1 {
		return fmt.Errorf("%w: bad issuance signature for %s", ErrInvalidInput, r.ID)
	}
	return nil
}

func (v HMACVerifier) VerifyEquality(p EqualityProof) error {
	seen := make(map[PartyID]struct{}, len(p.Signatures))
	for _, sig := range p.Signatures {
		if sig.Signer == "" || len(sig.Signature) == 0 {
			return fmt.Errorf("%w: empty equality signature", ErrInvalidInput)
		}
		if _, duplicate := seen[sig.Signer]; duplicate {
			return fmt.Errorf("%w: duplicate equality signer %s", ErrInvalidInput, sig.Signer)
		}
		seen[sig.Signer] = struct{}{}
		key, ok := v.EqualityKeys[sig.Signer]
		if !ok || len(key) == 0 {
			return fmt.Errorf("%w: no equality key for %s", ErrInvalidInput, sig.Signer)
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(equalitySigningBytes(p, sig.Signer))
		if subtle.ConstantTimeCompare(mac.Sum(nil), sig.Signature) != 1 {
			return fmt.Errorf("%w: bad equality signature for %s", ErrInvalidInput, sig.Signer)
		}
	}
	if len(seen) < 2 {
		return fmt.Errorf("%w: equality proof %s needs two distinct signers", ErrInvalidInput, p.ID)
	}
	return nil
}

// HMACAllocator deterministically derives fresh tokens from a secret. A host
// may use it as a safe default allocator.
type HMACAllocator struct {
	Secret []byte
	Prefix string
}

func (a HMACAllocator) Allocate(req AllocationRequest) (Token, error) {
	if len(a.Secret) == 0 {
		return "", fmt.Errorf("%w: empty allocator secret", ErrAllocatorRequired)
	}
	prefix := a.Prefix
	if prefix == "" {
		prefix = "tm1_"
	}
	w := newKeyedHashWriter(a.Secret, []byte("tokenmigrate/alloc/v1"))
	w.str(req.Scope.Domain.String())
	w.u64(uint64(req.Scope.Generation))
	w.str(string(req.RecordID))
	w.str(string(req.Commitment))
	w.str(string(req.OldToken))
	w.u64(uint64(req.Attempt))
	sum := w.sum()
	return Token(prefix + base64.RawURLEncoding.EncodeToString(sum[:18])), nil
}

// DeterministicAllocator is useful for reproducible tests. It is not secret;
// production hosts should use HMACAllocator or another unpredictable source.
type DeterministicAllocator struct {
	Prefix string
}

func (a DeterministicAllocator) Allocate(req AllocationRequest) (Token, error) {
	prefix := a.Prefix
	if prefix == "" {
		prefix = "test_"
	}
	w := newHashWriter([]byte("tokenmigrate/deterministic/v1"))
	w.str(req.Scope.Domain.String())
	w.u64(uint64(req.Scope.Generation))
	w.str(string(req.RecordID))
	w.str(string(req.Commitment))
	w.u64(uint64(req.Attempt))
	sum := w.sum()
	return Token(prefix + base64.RawURLEncoding.EncodeToString(sum[:18])), nil
}

// HMACIssuer signs changed issuance records using the target domain's key.
type HMACIssuer struct {
	Keys map[DomainID][]byte
}

func (i HMACIssuer) SignIssuance(r Issuance) ([]byte, error) {
	key, ok := i.Keys[r.Domain]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("%w: no signing key for %s", ErrInvalidInput, r.Domain)
	}
	out := r
	out.Signature = nil
	if err := SignIssuanceHMAC(key, &out); err != nil {
		return nil, err
	}
	return out.Signature, nil
}

func sortedStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}
