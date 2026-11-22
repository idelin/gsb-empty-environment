package tokenmigrate

import (
	"errors"
	"fmt"
	"time"
)

// Errors returned by the library. In particular, every failed compatibility
// read returns ErrNotFound. The library deliberately does not expose a
// distinguishable "exists but forbidden" error.
var (
	ErrNotFound          = errors.New("token not found")
	ErrInvalidInput      = errors.New("token migration: invalid input")
	ErrNoVerifier        = errors.New("token migration: signature verifier is required")
	ErrAllocatorRequired = errors.New("token migration: token allocator is required for reissues")
	ErrSignerRequired    = errors.New("token migration: signer is required for changed issuances")
	ErrInputMismatch     = errors.New("token migration: plan does not match input")
)

// DomainID identifies a tenant or tokenization domain.
type DomainID string

func (d DomainID) String() string { return string(d) }

// Generation identifies a token namespace within a domain.
type Generation uint64

// RecordID is the stable ID of an issuance record.
type RecordID string

// Token is an opaque token value.
type Token string

// Commitment is an irreversible commitment to an original value. The library
// treats it as an opaque string and never attempts to invert it.
type Commitment string

// ChangeID identifies a domain change.
type ChangeID string

// PartyID identifies a signer of an equality proof.
type PartyID string

// Period is a half-open validity interval [NotBefore, NotAfter). A zero bound
// is unbounded.
type Period struct {
	NotBefore time.Time
	NotAfter  time.Time
}

// Contains reports whether t is inside the interval.
func (p Period) Contains(t time.Time) bool {
	if !p.NotBefore.IsZero() && t.Before(p.NotBefore) {
		return false
	}
	if !p.NotAfter.IsZero() && !t.Before(p.NotAfter) {
		return false
	}
	return true
}

// Valid reports whether the interval is well formed.
func (p Period) Valid() bool {
	if p.NotBefore.IsZero() || p.NotAfter.IsZero() {
		return true
	}
	return p.NotBefore.Before(p.NotAfter)
}

// Intersect returns the intersection of two periods.
func (p Period) Intersect(other Period) Period {
	out := p
	if out.NotBefore.IsZero() || (!other.NotBefore.IsZero() && other.NotBefore.After(out.NotBefore)) {
		out.NotBefore = other.NotBefore
	}
	if out.NotAfter.IsZero() || (!other.NotAfter.IsZero() && other.NotAfter.Before(out.NotAfter)) {
		out.NotAfter = other.NotAfter
	}
	return out
}

// Scope is the namespace in which a token is unique.
type Scope struct {
	Domain     DomainID
	Generation Generation
}

func (s Scope) String() string {
	return fmt.Sprintf("%s/%d", s.Domain, s.Generation)
}

// Domain is one member of the finite domain set.
type Domain struct {
	ID    DomainID
	Valid Period
}

// Issuance is a signature-bound issuance record.
type Issuance struct {
	ID         RecordID
	Domain     DomainID
	Generation Generation
	Token      Token
	Commitment Commitment
	Valid      Period
	Signature  []byte
}

// Scope returns the namespace in which the issuance token is unique.
func (r Issuance) Scope() Scope {
	return Scope{Domain: r.Domain, Generation: r.Generation}
}

// Shard is a bidirectional token/commitment index for one domain generation.
//
// TokenToCommitment is a function: a token has at most one commitment in a
// scope. CommitmentToTokens is a reverse index and may contain aliases: two
// tokens may point to the same commitment.
type Shard struct {
	ID                 string
	Domain             DomainID
	Generation         Generation
	TokenToCommitment  map[Token]Commitment
	CommitmentToTokens map[Commitment][]Token
}

// Scope returns the shard's namespace.
func (s Shard) Scope() Scope {
	return Scope{Domain: s.Domain, Generation: s.Generation}
}

// ChangeKind describes a merge or a split.
type ChangeKind uint8

const (
	// MergeChange moves source domains, or explicitly selected subsets, into
	// one target domain.
	MergeChange ChangeKind = iota + 1
	// SplitChange moves explicitly authorized record subsets to child domains.
	SplitChange
)

func (k ChangeKind) String() string {
	switch k {
	case MergeChange:
		return "merge"
	case SplitChange:
		return "split"
	default:
		return "unknown"
	}
}

// Mapping is one source-to-target edge of a domain change.
//
// For an explicit mapping, Records is the complete authorization set. An
// explicit empty set is valid and means that the target receives no records.
// Generation zero means "keep the source record's generation".
type Mapping struct {
	From       DomainID
	To         DomainID
	Generation Generation
	Explicit   bool
	Records    []RecordID
}

// Change is a time-bounded domain change.
type Change struct {
	ID       ChangeID
	Kind     ChangeKind
	Valid    Period
	Mappings []Mapping
}

// EqualitySignature is one party's signature over an EqualityProof.
type EqualitySignature struct {
	Signer    PartyID
	Signature []byte
}

// EqualityProof asserts that two commitments represent the same original
// value. A proof is usable only after both signatures have been verified.
type EqualityProof struct {
	ID         string
	Left       Commitment
	Right      Commitment
	Signatures []EqualitySignature
}

// Input is the complete migration input.
type Input struct {
	Domains []Domain
	Changes []Change
	Shards  []Shard
	Records []Issuance
	Proofs  []EqualityProof
}

// Verifier authenticates issuance records and equality proofs. The host is
// responsible for key management; HMACVerifier is supplied for hosts whose
// signatures are HMAC based.
type Verifier interface {
	VerifyIssuance(Issuance) error
	VerifyEquality(EqualityProof) error
}

// VerifierFunc adapts functions to Verifier.
type VerifierFunc struct {
	Issuance func(Issuance) error
	Equality func(EqualityProof) error
}

func (v VerifierFunc) VerifyIssuance(r Issuance) error {
	if v.Issuance == nil {
		return ErrNoVerifier
	}
	return v.Issuance(r)
}

func (v VerifierFunc) VerifyEquality(p EqualityProof) error {
	if v.Equality == nil {
		return ErrNoVerifier
	}
	return v.Equality(p)
}

// PermissiveVerifier accepts every issuance signature. Equality proofs must
// still carry two non-empty signatures from distinct parties. It is intended
// only for tests and local model checking; production hosts must use a real
// Verifier.
type PermissiveVerifier struct{}

func (PermissiveVerifier) VerifyIssuance(Issuance) error { return nil }

func (PermissiveVerifier) VerifyEquality(p EqualityProof) error {
	seen := make(map[PartyID]struct{}, len(p.Signatures))
	for _, sig := range p.Signatures {
		if sig.Signer == "" || len(sig.Signature) == 0 {
			return fmt.Errorf("%w: empty equality signature", ErrInvalidInput)
		}
		seen[sig.Signer] = struct{}{}
	}
	if len(seen) < 2 {
		return fmt.Errorf("%w: equality proof %s needs two distinct signers", ErrInvalidInput, p.ID)
	}
	return nil
}

// AllocationRequest describes one fresh target token allocation.
type AllocationRequest struct {
	Scope       Scope
	RecordID    RecordID
	Commitment  Commitment
	OldToken    Token
	Attempt     int
	Unavailable []Token
}

// TokenAllocator allocates an unpredictable token outside Unavailable.
type TokenAllocator interface {
	Allocate(AllocationRequest) (Token, error)
}

// AllocatorFunc adapts a function to TokenAllocator.
type AllocatorFunc func(AllocationRequest) (Token, error)

func (f AllocatorFunc) Allocate(req AllocationRequest) (Token, error) {
	return f(req)
}

// IssuanceSigner signs changed target issuance records.
type IssuanceSigner interface {
	SignIssuance(Issuance) ([]byte, error)
}

// SignerFunc adapts a function to IssuanceSigner.
type SignerFunc func(Issuance) ([]byte, error)

func (f SignerFunc) SignIssuance(r Issuance) ([]byte, error) {
	return f(r)
}

// Options controls planning and materialization.
type Options struct {
	// At selects the active domain changes. A zero value means time.Now().UTC().
	At time.Time
	// Verifier is mandatory.
	Verifier Verifier
	// Allocator is mandatory only when at least one record must be reissued.
	Allocator TokenAllocator
	// Issuer is mandatory only when a record's signed payload changes.
	Issuer IssuanceSigner
}

// Assignment is the final placement of one live record.
type Assignment struct {
	RecordID RecordID
	Origin   Scope
	Target   Scope

	OldToken Token
	NewToken Token

	Commitment Commitment
	// Class is the canonical commitment class after verified equalities.
	Class Commitment

	Moved     bool
	Reissued  bool
	Canonical bool
}

func (a Assignment) changed() bool {
	return a.Moved || a.Reissued || a.Canonical
}

// Reissue describes one record whose target token differs from its old token.
type Reissue struct {
	RecordID RecordID
	Origin   Scope
	OldToken Token
	Target   Scope
	NewToken Token
	Class    Commitment
}

// ReadResult is a successful compatibility read.
type ReadResult struct {
	Tenant     DomainID
	Origin     Scope
	OldToken   Token
	Target     Scope
	Token      Token
	Commitment Commitment
	Class      Commitment
	RecordIDs  []RecordID
	ValidFrom  time.Time
	ValidUntil time.Time
}

// ReadRequest asks the compatibility table to redeem an old, scoped token.
type ReadRequest struct {
	Tenant DomainID
	Origin Scope
	Token  Token
	At     time.Time
}

// Snapshot is a materialized migration result. It is a new value; the input
// used to create it is never modified.
type Snapshot struct {
	At      time.Time
	Records []Issuance
	Shards  []Shard
}

// Result is returned by Migrate.
type Result struct {
	Plan     *Plan
	Snapshot Snapshot
}
