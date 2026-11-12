package netsec

import "context"

// ZeroOID is the all-zero object identifier Git uses to signal the absence of
// a commit, i.e. the old value of a newly created ref or the new value of a
// deleted ref.
const ZeroOID OID = ""

// OID is a Git object identifier (object hash). It is kept as an opaque
// string so the package does not depend on a particular hashing backend.
type OID string

// IsZero reports whether o is the absent-object sentinel.
func (o OID) IsZero() bool {
	return o == ZeroOID
}

// RefUpdate is one line of a pre-receive push: a ref moving from Old to New.
type RefUpdate struct {
	Ref string
	Old OID
	New OID
}

// IsCreation reports whether the update creates a new ref (old is zero).
func (u RefUpdate) IsCreation() bool {
	return u.Old.IsZero() && !u.New.IsZero()
}

// IsDeletion reports whether the update deletes a ref (new is zero).
func (u RefUpdate) IsDeletion() bool {
	return u.New.IsZero()
}

// Commit describes the graph metadata and storage size the policy needs from
// a single commit object. Hosting platforms implement this over their own
// object storage; tests may use MemoryCommit.
type Commit interface {
	// ID returns the object identifier of the commit.
	ID() OID
	// Parents returns every parent of the commit, including merge parents.
	// Returning all parents is what lets the policy inspect the full commit
	// graph instead of only the first-parent line.
	Parents() []OID
	// Size returns the on-disk object byte size counted against the quota.
	Size() int64
}

// Repository gives the policy read access to the object database and ref
// graph of the receiving repository.
//
// LookupCommit returns an error wrapping ErrObjectNotFound when the object is
// absent from the object database; implementations may return any other error
// for storage failures, which fail the admission closed.
type Repository interface {
	LookupCommit(ctx context.Context, id OID) (Commit, error)
}

// VerifySignature inspects a commit's cryptographic signature.
//
// It returns the identity (e.g. key fingerprint or email) of the signer when
// the commit carries a valid, verifiable signature. It returns a non-nil
// error when the commit is unsigned, malformed, signed by an untrusted key, or
// fails verification. The host supplies this callback so the policy stays
// agnostic to the signing backend.
type VerifySignature func(ctx context.Context, c Commit) (signer string, err error)

// ErrObjectNotFound is returned (wrapped) when a referenced commit object is
// missing from the object database.
var ErrObjectNotFound = admissionError("netsec: commit object not found")

// ErrUnsignedCommit indicates a commit carries no valid signature.
var ErrUnsignedCommit = admissionError("netsec: commit is not signed")

// ErrSignerNotAllowed indicates a valid signature came from a signer that is
// not on the allowed-signer list.
var ErrSignerNotAllowed = admissionError("netsec: signer is not allowed")

// ErrQuotaExhausted indicates a push needs more object bytes than the shared
// repository quota can currently provide.
var ErrQuotaExhausted = admissionError("netsec: object byte quota exhausted")

// admissionError is an immutable sentinel error so the exported errors support
// errors.Is directly.
type admissionError string

func (e admissionError) Error() string { return string(e) }
