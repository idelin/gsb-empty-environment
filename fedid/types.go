package fedid

import "errors"

var (
	ErrProofInvalid    = errors.New("fedid: proof token invalid, expired or already consumed")
	ErrIdentityBound   = errors.New("fedid: identity already bound to another account")
	ErrAccountNotFound = errors.New("fedid: account not found")
	ErrUnknownIdentity = errors.New("fedid: no identity for verified issuer+sub")
	ErrProofMismatch   = errors.New("fedid: proof does not match its intended subject")
	ErrCommitFailed    = errors.New("fedid: transaction commit failed")
)

// IdentityKey is the primary key of a federated identity: the verified
// issuer and sub claims. Email is never part of the key.
type IdentityKey struct {
	Issuer string
	Sub    string
}

// Identity is a verified federated identity bound to exactly one account.
type Identity struct {
	Key       IdentityKey
	Email     string // verified email reported by the IdP; non-identifying
	AccountID string
}

// Account owns one or more identities.
type Account struct {
	ID         string
	Identities []IdentityKey
}

// ProofKind distinguishes the two one-time proofs required for binding.
type ProofKind int

const (
	// ProofAccount proves control of the existing account (e.g. a fresh
	// login session of an already-bound identity).
	ProofAccount ProofKind = iota
	// ProofIdentity proves control of the new identity to be bound.
	ProofIdentity
)

// Proof is a single-use token. Consumption is atomic: the same token can
// be consumed successfully exactly once across all concurrent transactions.
type Proof struct {
	Token     string
	Kind      ProofKind
	AccountID string      // set for ProofAccount
	Key       IdentityKey // set for ProofIdentity
	Email     string      // verified email of the new identity
	Consumed  bool
}
