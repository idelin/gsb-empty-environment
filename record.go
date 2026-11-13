package netsec

import (
	"bytes"
	"fmt"
)

// PrincipalID identifies a caller.  It is deliberately only an identifier:
// recovery never resolves an identifier to current permissions.
type PrincipalID = string

// ObjectID identifies an object number.  Object numbers may be reused with a
// new generation, so an ObjectID alone is never an authorization identity.
type ObjectID = string

// TxID identifies a single authorization transaction.
type TxID = string

// Operation is an opaque operation name.  Common values are Write, Delete and
// Metadata.
type Operation = string

// Stage is a durable step in the transaction log.
type Stage = string

const (
	StagePrepare    Stage = "Prepare"
	StageDataStable Stage = "DataStable"
	StageCommit     Stage = "Commit"
	// StagePublished is a host callback stage, not a log record stage.
	StagePublished Stage = "Published"
)

const (
	OpWrite    Operation = "Write"
	OpDelete   Operation = "Delete"
	OpMetadata Operation = "Metadata"
)

// ObjectRef is an immutable object identity: object number plus generation.
type ObjectRef struct {
	ID         ObjectID `json:"objectID"`
	Generation uint64   `json:"generation"`
}

// TxIdentity contains every field that makes an authorization decision
// specific to one immutable operation.  In particular it contains object
// generation, the expected old metadata digest, the new metadata digest and
// the parameter digest.  An "allow" bit alone is never sufficient.
type TxIdentity struct {
	TxID                 TxID        `json:"txID"`
	PrincipalID          PrincipalID `json:"principalID"`
	CredentialGeneration uint64      `json:"credentialGeneration"`
	PolicyDigest         Digest      `json:"policyDigest"`
	ObjectID             ObjectID    `json:"objectID"`
	ObjectGeneration     uint64      `json:"objectGeneration"`
	Operation            Operation   `json:"operation"`
	ParamsDigest         Digest      `json:"paramsDigest"`
	PatchDigest          Digest      `json:"patchDigest"`
	OldMetadataDigest    Digest      `json:"oldMetadataDigest"`
	NewMetadataDigest    Digest      `json:"newMetadataDigest"`
	DataDigest           Digest      `json:"dataDigest"`
}

// IdentityDigest hashes the complete immutable operation identity.
func (i TxIdentity) IdentityDigest() (Digest, error) {
	return canonicalHash("netsec/tx-identity/v1", i)
}

// Record is one signed entry in the transaction log.  Every record repeats
// the complete transaction identity so that a stage can never be paired with
// different parameters or a different object generation.
type Record struct {
	Seq      uint64     `json:"seq"`
	Prev     Digest     `json:"prev"`
	Stage    Stage      `json:"stage"`
	Identity TxIdentity `json:"identity"`

	// AuthReceiptDigest binds the authorization allow decision to the
	// complete identity and policy.  Recovery verifies this digest and the
	// host HMAC; it never performs a principal-name policy lookup.
	AuthReceiptDigest Digest `json:"authReceiptDigest"`

	// Patch is the metadata patch that must be applied by the host after
	// the old metadata digest has been observed.  NewMetadataDigest is the
	// digest of the result.
	Patch []byte `json:"patch,omitempty"`
}

// ComputeAuthReceiptDigest produces a host receipt for an allowed immutable
// operation.  It cannot be transplanted to another object, generation,
// parameter set, policy or credential generation because all of those values
// are part of the identity digest.
func ComputeAuthReceiptDigest(identity TxIdentity, policyDigest Digest, allowed bool) (Digest, error) {
	identityDigest, err := identity.IdentityDigest()
	if err != nil {
		return ZeroDigest, err
	}
	decision := []byte{0}
	if allowed {
		decision[0] = 1
	}
	return hashParts("netsec/auth-receipt/v1",
		[]byte(identityDigest.String()),
		[]byte(policyDigest.String()),
		decision,
	), nil
}

// TxDigest is the digest of the transaction identity repeated in a record.
func (r Record) TxDigest() (Digest, error) {
	return r.Identity.IdentityDigest()
}

func (r Record) validStage() bool {
	switch r.Stage {
	case StagePrepare, StageDataStable, StageCommit:
		return true
	default:
		return false
	}
}

// Validate checks fields that can be checked without the host HMAC.
func (r Record) Validate() error {
	if !r.validStage() {
		return fmt.Errorf("%w: unknown stage %q", ErrInvalidRecord, r.Stage)
	}
	if r.Identity.TxID == "" {
		return fmt.Errorf("%w: empty transaction id", ErrInvalidRecord)
	}
	if r.Identity.PrincipalID == "" {
		return fmt.Errorf("%w: empty principal id", ErrInvalidRecord)
	}
	if r.Identity.ObjectID == "" {
		return fmt.Errorf("%w: empty object id", ErrInvalidRecord)
	}
	if r.Identity.Operation == "" {
		return fmt.Errorf("%w: empty operation", ErrInvalidRecord)
	}
	if r.AuthReceiptDigest.IsZero() {
		return fmt.Errorf("%w: missing authorization receipt digest", ErrInvalidRecord)
	}
	want, err := ComputeAuthReceiptDigest(r.Identity, r.Identity.PolicyDigest, true)
	if err != nil {
		return err
	}
	if want != r.AuthReceiptDigest {
		return fmt.Errorf("%w: authorization receipt does not match operation identity", ErrInvalidRecord)
	}
	if got := DigestBytes(r.Patch); got != r.Identity.PatchDigest {
		return fmt.Errorf("%w: metadata patch digest mismatch", ErrInvalidRecord)
	}
	return nil
}

// SameIdentity reports whether two records belong to the same immutable
// operation.  It deliberately ignores the stage, sequence and chain links.
func SameIdentity(a, b Record) bool {
	ad, err := a.TxDigest()
	if err != nil {
		return false
	}
	bd, err := b.TxDigest()
	if err != nil {
		return false
	}
	return a.Identity.TxID == b.Identity.TxID &&
		ad == bd &&
		a.AuthReceiptDigest == b.AuthReceiptDigest &&
		bytes.Equal(a.Patch, b.Patch)
}
