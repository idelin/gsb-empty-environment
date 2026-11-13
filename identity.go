package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Token is one model tokenizer token id. The cache deliberately treats tokens
// as opaque uint32 values; the tokenizer digest in Identity names the tokenizer
// that gives the ids their meaning.
type Token uint32

// Identity is the complete security identity of a cached KV prefix. A hit is
// possible only when every field below is equal and the entry's content MAC
// verifies.
//
// PublicScope is the one explicit exception to tenant isolation. When it is
// non-empty the prefix is public only for the configured public scope and the
// tenant id is normalized out of the canonical identity. All other fields,
// including the canonical token-prefix digest, remain bound.
type Identity struct {
	TenantID               string
	ModelDigest            string
	AdapterDigest          string
	TokenizerDigest        string
	DecodePosition         int64
	TokenPrefixDigest      [32]byte
	SafetyPolicyGeneration uint64
	KVLayoutVersion        uint32
	PublicScope            string
}

// TokenRange is the half-open range of absolute token positions covered by an
// entry. Entries are immutable: extending a prefix produces a new range.
type TokenRange struct {
	Start int64
	End   int64
}

// Length returns End-Start.
func (r TokenRange) Length() int64 { return r.End - r.Start }

// Contains reports whether r contains all of other.
func (r TokenRange) Contains(other TokenRange) bool {
	return other.Start >= r.Start && other.End <= r.End
}

// CanonicalTokenPrefixDigest computes the canonical digest of a token prefix.
// The promptDigest argument is an optional host summary of a private system
// prompt. When supplied it is folded into the digest, so the same user-visible
// token ids with a different private system prompt do not share an identity.
//
// The encoding is length-prefixed and fixed width. It therefore has no
// ambiguous concatenation forms and does not depend on host endianness.
func CanonicalTokenPrefixDigest(tokens []Token, promptDigest [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("netsec.kvprefix.tokens.v1\x00"))
	_, _ = h.Write(promptDigest[:])
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(tokens)))
	_, _ = h.Write(lenBuf[:])
	var tokBuf [4]byte
	for _, tok := range tokens {
		binary.BigEndian.PutUint32(tokBuf[:], uint32(tok))
		_, _ = h.Write(tokBuf[:])
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

func appendBytes(dst []byte, b []byte) []byte {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	dst = append(dst, lenBuf[:]...)
	dst = append(dst, b...)
	return dst
}

func appendUint64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

func appendUint32(dst []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(dst, buf[:]...)
}

// Canonical encodes the identity with explicit domains and length prefixes.
// For private entries TenantID is bound. For a public scope TenantID is
// deliberately not encoded; PublicScope is bound in its place. This is the
// only way two different tenants can share an entry.
func (id Identity) Canonical() []byte {
	buf := make([]byte, 0, 256)
	buf = append(buf, []byte("netsec.kvprefix.identity.v1\x00")...)
	if id.PublicScope == "" {
		buf = append(buf, 0)
		buf = appendBytes(buf, []byte(id.TenantID))
	} else {
		buf = append(buf, 1)
		buf = appendBytes(buf, []byte(id.PublicScope))
	}
	buf = appendBytes(buf, []byte(id.ModelDigest))
	buf = appendBytes(buf, []byte(id.AdapterDigest))
	buf = appendBytes(buf, []byte(id.TokenizerDigest))
	buf = appendUint64(buf, uint64(id.DecodePosition))
	buf = append(buf, id.TokenPrefixDigest[:]...)
	buf = appendUint64(buf, id.SafetyPolicyGeneration)
	buf = appendUint32(buf, id.KVLayoutVersion)
	return buf
}

// Digest returns the SHA-256 of the canonical identity encoding.
func (id Identity) Digest() [32]byte { return sha256.Sum256(id.Canonical()) }

// Request describes a prefix lookup or build request. Tokens starts at the
// absolute position Position. If PromptDigest is non-zero it is folded into
// the canonical token-prefix digest.
type Request struct {
	TenantID               string
	ModelDigest            string
	AdapterDigest          string
	TokenizerDigest        string
	Position               int64
	Tokens                 []Token
	PromptDigest           [32]byte
	SafetyPolicyGeneration uint64
	KVLayoutVersion        uint32
	PublicScope            string
}

// TokenPrefixDigest returns the canonical digest for the request.
func (r Request) TokenPrefixDigest() [32]byte {
	return CanonicalTokenPrefixDigest(r.Tokens, r.PromptDigest)
}

// Identity materializes the canonical identity of the request.
func (r Request) Identity() Identity {
	tenant := r.TenantID
	if r.PublicScope != "" {
		// A public entry is deliberately tenantless in its shared identity.
		// The creator's tenant is not part of the cache key and is not exposed
		// through a lease handed to another tenant.
		tenant = ""
	}
	return Identity{
		TenantID:               tenant,
		ModelDigest:            r.ModelDigest,
		AdapterDigest:          r.AdapterDigest,
		TokenizerDigest:        r.TokenizerDigest,
		DecodePosition:         r.Position,
		TokenPrefixDigest:      r.TokenPrefixDigest(),
		SafetyPolicyGeneration: r.SafetyPolicyGeneration,
		KVLayoutVersion:        r.KVLayoutVersion,
		PublicScope:            r.PublicScope,
	}
}

// Range returns the token range described by the request.
func (r Request) Range() (TokenRange, error) {
	if len(r.Tokens) == 0 {
		return TokenRange{}, errors.New("netsec: token prefix must not be empty")
	}
	if r.Position < 0 {
		return TokenRange{}, errors.New("netsec: decode position must not be negative")
	}
	if int64(len(r.Tokens)) > math.MaxInt64-r.Position {
		return TokenRange{}, errors.New("netsec: token range overflows int64")
	}
	return TokenRange{Start: r.Position, End: r.Position + int64(len(r.Tokens))}, nil
}

func (r Request) validate() (Identity, TokenRange, error) {
	if r.ModelDigest == "" {
		return Identity{}, TokenRange{}, errors.New("netsec: model digest is required")
	}
	if r.TokenizerDigest == "" {
		return Identity{}, TokenRange{}, errors.New("netsec: tokenizer digest is required")
	}
	if r.KVLayoutVersion == 0 {
		return Identity{}, TokenRange{}, errors.New("netsec: KV layout version must be non-zero")
	}
	if r.KVLayoutVersion > 1<<16 {
		return Identity{}, TokenRange{}, fmt.Errorf("netsec: KV layout version %d is too large", r.KVLayoutVersion)
	}
	if r.PublicScope == "" && r.TenantID == "" {
		return Identity{}, TokenRange{}, errors.New("netsec: tenant id is required for private prefixes")
	}
	rng, err := r.Range()
	if err != nil {
		return Identity{}, TokenRange{}, err
	}
	return r.Identity(), rng, nil
}

// PublicScope registers one explicit public sharing scope. Only the listed
// canonical token-prefix digests may be marked public. A host should add these
// entries only for prefixes it has deliberately decided are non-secret.
type PublicScope struct {
	Name               string
	TokenPrefixDigests [][32]byte
}
