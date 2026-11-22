// Package redemption implements an importable transaction acceptance kit for
// an anonymous credential redemption host.
//
// Holders collect authorization notes of integer denominations from issuers
// and redeem them against services. A transaction spends a set of input notes
// (issued notes or change outputs of earlier transactions) and creates service
// entitlements plus change notes. The cryptographic material (issuer
// signatures on notes and zero-knowledge redemption proofs) is opaque to this
// package: the host injects the verifiers it trusts. Everything else —
// conservation, per-issuer/activity limits, nullifier double-spend rules,
// dependency closure and acyclicity — is checked here.
//
// The selection problem (at most 22 candidate transactions) is solved by
// branch-and-bound and the search emits an independently checkable
// optimality certificate (see cert.go). A separate full-enumeration routine
// (brute.go) is used by the tests and serves as an independent oracle.
package redemption

import "errors"

// IssuerID identifies an issuing authority.
type IssuerID string

// ActivityID identifies an activity generation ("活动代次"). Conservation and
// quotas are scoped per activity; value never moves between activities.
type ActivityID string

// Commitment is the public commitment value of a note. It is the only
// value-bearing identifier revealed on the chain besides issuer, activity,
// integer denomination and the one-time nullifier.
type Commitment string

// Nullifier is the one-time redemption identifier ("一次性核销标识"). The same
// nullifier may appear in competing transaction branches, but an accepted set
// may spend it at most once.
type Nullifier string

// TxID identifies a candidate or confirmed transaction.
type TxID string

// ServiceID identifies a service an entitlement is delivered against.
type ServiceID string

// BalanceKey partitions value conservation and remaining-quotas: one
// (issuer, activity) pair. Value can never be moved between keys.
type BalanceKey struct {
	Issuer   IssuerID   `json:"issuer"`
	Activity ActivityID `json:"activity"`
}

// Outpoint references a note created by a transaction output.
type Outpoint struct {
	TxID     TxID  `json:"tx_id"`
	OutIndex uint8 `json:"out_index"`
}

// NoteSource describes where an input note comes from.
//
//   - Issued (Origin non-nil): an authorization note issued directly by an
//     issuer. Outpoint must be nil.
//   - Change (Outpoint non-nil): a change output of another transaction.
//     Origin must be nil.
type NoteSource struct {
	Origin   *OriginNote `json:"origin,omitempty"`
	Outpoint *Outpoint   `json:"outpoint,omitempty"`
}

// OriginNote is an issuer-minted authorization note. Only privacy-preserving
// fields are modeled: issuer, activity, public commitment, integer
// denomination and the one-time nullifier. The issuer signature is carried
// opaquely and verified through the host-injected SignatureVerifier.
type OriginNote struct {
	Issuer       IssuerID   `json:"issuer"`
	Activity     ActivityID `json:"activity"`
	Commitment   Commitment `json:"commitment"`
	Denomination int64      `json:"denomination"`
	Nullifier    Nullifier  `json:"nullifier"`
	Signature    []byte     `json:"signature,omitempty"`
}

// Input is a note spent by a transaction. Amount, Issuer, Activity and
// Commitment must match the referenced note; they are repeated here so a
// transaction is self-describing for import.
type Input struct {
	Source     NoteSource `json:"source"`
	Issuer     IssuerID   `json:"issuer"`
	Activity   ActivityID `json:"activity"`
	Commitment Commitment `json:"commitment"`
	Amount     int64      `json:"amount"`
	Nullifier  Nullifier  `json:"nullifier"`
}

// ChangeOutput is a change note created by a transaction. It may later be
// referenced as an input through Outpoint{TxID, OutIndex}.
type ChangeOutput struct {
	Issuer     IssuerID   `json:"issuer"`
	Activity   ActivityID `json:"activity"`
	Commitment Commitment `json:"commitment"`
	Amount     int64      `json:"amount"`
}

// ServiceEntitlement is a delivered service right. Units is the number of
// service rights completed; Cost is the integer value consumed per
// balance key, summed over all entitlements of the transaction.
type ServiceEntitlement struct {
	Service  ServiceID  `json:"service"`
	Issuer   IssuerID   `json:"issuer"`
	Activity ActivityID `json:"activity"`
	Units    int64      `json:"units"`
	Cost     int64      `json:"cost"`
}

// Transaction is a candidate or confirmed redemption transaction.
//
// The cryptographic Proof binds the input commitments to this transaction,
// its output entitlements and change commitments. It is verified by the
// host-injected ProofVerifier; this package never inspects its contents.
type Transaction struct {
	ID      TxID                 `json:"id"`
	Inputs  []Input              `json:"inputs"`
	Outputs []ChangeOutput       `json:"outputs"`
	Spends  []ServiceEntitlement `json:"spends"`
	Proof   []byte               `json:"proof,omitempty"`
}

// Quota is the remaining redemption limit for one issuer in one activity.
// Keys absent from the quota table are treated as zero remaining limit.
type Quota struct {
	Issuer   IssuerID   `json:"issuer"`
	Activity ActivityID `json:"activity"`
	Limit    int64      `json:"limit"`
}

// Problem is the importable transaction acceptance instance.
//
// Confirmed is the final confirmed transaction prefix: it is always
// considered accepted. Candidates conflicting with the prefix (spending an
// already-spent nullifier or outpoint, or claiming different note data) are
// rejected. Candidates is the unconfirmed transaction set; CandidateIDs must
// be unique and disjoint from confirmed IDs.
type Problem struct {
	Issuers    []IssuerID    `json:"issuers"`
	Quotas     []Quota       `json:"quotas"`
	Confirmed  []Transaction `json:"confirmed"`
	Candidates []Transaction `json:"candidates"`
}

// Solution is the accepted candidate subset together with the optimality
// certificate.
type Solution struct {
	Selected     []TxID       `json:"selected"`
	Entitlements int64        `json:"entitlements"`
	Certificate  *Certificate `json:"certificate"`
}

// Errors returned by validation and verification routines.
var (
	ErrMalformed         = errors.New("redemption: malformed problem or transaction")
	ErrConservation      = errors.New("redemption: per-activity conservation violated")
	ErrUnknownNote       = errors.New("redemption: input references unknown note")
	ErrNoteMismatch      = errors.New("redemption: input does not match referenced note")
	ErrDoubleSpend       = errors.New("redemption: nullifier or outpoint spent more than once")
	ErrSpentInPrefix     = errors.New("redemption: note already spent in confirmed prefix")
	ErrMissingDependency = errors.New("redemption: selected transaction misses an unconfirmed predecessor")
	ErrCycle             = errors.New("redemption: dependency cycle cannot self-fund")
	ErrQuotaExceeded     = errors.New("redemption: remaining issuer quota exceeded")
	ErrSignature         = errors.New("redemption: note signature verification failed")
	ErrProof             = errors.New("redemption: redemption proof verification failed")
	ErrCertificate       = errors.New("redemption: invalid optimality certificate")
	ErrDigestMismatch    = errors.New("redemption: certificate binds a different problem")
)
