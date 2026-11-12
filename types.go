package netsec

import (
	"crypto/sha256"
	"encoding/hex"
)

// AccountID identifies a tenant account. Aliases are only meaningful within
// the scope of an account and a region; the same alias name in another account
// or region is a different reference and may bind different key material.
type AccountID string

// Region is the deployment region in which an alias binding is valid.
type Region string

// AliasName is a local, non-global alias identifier.
type AliasName string

// KeyID identifies key material inside an account.
type KeyID string

// Fingerprint is the lowercase hex SHA-256 digest of the DER-encoded public key.
type Fingerprint string

// SHA256Fingerprint returns the canonical fingerprint of rawPublicKey, which
// callers should supply as the DER-encoded SubjectPublicKeyInfo (or any other
// stable wire encoding used consistently across the deployment).
func SHA256Fingerprint(rawPublicKey []byte) Fingerprint {
	sum := sha256.Sum256(rawPublicKey)
	return Fingerprint(hex.EncodeToString(sum[:]))
}

// AliasRef is a fully qualified alias reference. It is the identity used by the
// resolver; an alias name alone is never treated as a global identity.
type AliasRef struct {
	Account AccountID `json:"account"`
	Region  Region    `json:"region"`
	Name    AliasName `json:"name"`
}

// Valid reports whether the reference has all scoping components populated.
func (r AliasRef) Valid() bool {
	return r.Account != "" && r.Region != "" && r.Name != ""
}

// TargetKind describes what an alias binding points at.
type TargetKind string

const (
	// TargetAlias means the binding points at another alias reference,
	// possibly in another authorized account.
	TargetAlias TargetKind = "alias"
	// TargetKey means the binding terminates at concrete key material.
	TargetKey TargetKind = "key"
)

// Target is the payload of a binding. Exactly one of Alias or Key is
// meaningful according to Kind.
type Target struct {
	Kind  TargetKind `json:"kind"`
	Alias *AliasRef  `json:"alias,omitempty"`
	Key   KeyID      `json:"key,omitempty"`
}

// Binding is one immutable version of an alias binding. Rebinding an alias
// creates a new Binding with a monotonically increasing Version; the version
// observed at the start of a transaction is the version recorded in its
// resolution results.
type Binding struct {
	Ref     AliasRef `json:"ref"`
	Version uint64   `json:"version"`
	Target  Target   `json:"target"`
}

// PublicKey is the concrete key material an alias chain terminates at.
type PublicKey struct {
	Account     AccountID   `json:"account"`
	ID          KeyID       `json:"id"`
	Region      Region      `json:"region"`
	Fingerprint Fingerprint `json:"fingerprint"`
	DER         []byte      `json:"der,omitempty"`
}

// Hop records one alias traversal for auditing.
type Hop struct {
	Ref     AliasRef `json:"ref"`
	Version uint64   `json:"version"`
	Target  Target   `json:"target"`
}

// Resolution is the fully resolved outcome of one alias reference.
type Resolution struct {
	// Entry is the reference the caller asked to resolve.
	Entry AliasRef `json:"entry"`
	// EntryVersion is the binding version observed for Entry inside the
	// transaction (zero when Entry itself is never bound).
	EntryVersion uint64 `json:"entry_version"`
	// FinalAccount is the account that owns the terminating key material.
	FinalAccount AccountID `json:"final_account"`
	// FinalRegion is the region in which the terminating key is held.
	FinalRegion Region `json:"final_region"`
	// FinalKey identifies the terminating key material.
	FinalKey KeyID `json:"final_key"`
	// FinalFingerprint is the fingerprint the signer must use.
	FinalFingerprint Fingerprint `json:"final_fingerprint"`
	// Hops is the ordered chain of alias bindings that was traversed.
	Hops []Hop `json:"hops"`
}

// CallerIdentity represents an authenticated build-service caller and the set
// of accounts it is authorized to follow alias chains into.
type CallerIdentity struct {
	ID         string      `json:"id"`
	Authorized []AccountID `json:"authorized_accounts"`
	authorized map[AccountID]struct{}
}

// NewCallerIdentity builds a CallerIdentity and indexes the authorized
// accounts for constant-time authorization checks.
func NewCallerIdentity(id string, authorizedAccounts []AccountID) CallerIdentity {
	c := CallerIdentity{ID: id, Authorized: append([]AccountID(nil), authorizedAccounts...)}
	c.authorized = make(map[AccountID]struct{}, len(authorizedAccounts))
	for _, account := range c.Authorized {
		c.authorized[account] = struct{}{}
	}
	return c
}

// AuthorizedTo reports whether the caller may traverse bindings in account.
func (c CallerIdentity) AuthorizedTo(account AccountID) bool {
	_, ok := c.authorized[account]
	return ok
}
