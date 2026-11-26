package credentialauth

import (
	"sync"
)

type issuedRecord struct {
	requestDigest string
	proof         *Proof
}

// Verifier is a concurrency-safe, in-process issuance gate.  It remembers the
// first mutually exclusive lock issued for a snapshot digest.  A repeated
// verification of the same snapshot and request returns the same proof; a
// different request for that snapshot is rejected instead of issuing a second
// conflicting credential.
type Verifier struct {
	mu     sync.Mutex
	issued map[string]issuedRecord
}

// NewVerifier creates an empty issuance gate.
func NewVerifier() *Verifier {
	return &Verifier{issued: make(map[string]issuedRecord)}
}

// New is an alias for NewVerifier.
func New() *Verifier {
	return NewVerifier()
}

// Verify builds a proof for snapshot.  If the snapshot contains a request, the
// resulting lock is recorded idempotently.
func (v *Verifier) Verify(snapshot Snapshot) (*Proof, error) {
	proof, err := BuildProof(snapshot)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if normalized.request == nil {
		return proof, nil
	}
	return v.remember(normalized.snapshotDigest, normalized.requestDigest, proof)
}

// VerifyAndIssue builds and records a lock for request.
func (v *Verifier) VerifyAndIssue(snapshot Snapshot, request PermitRequest) (*Proof, error) {
	proof, err := verifyAndIssue(snapshot, request)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeRequestSnapshot(snapshot, request)
	if err != nil {
		return nil, err
	}
	return v.remember(normalized.snapshotDigest, normalized.requestDigest, proof)
}

// Issue is an alias for VerifyAndIssue.
func (v *Verifier) Issue(snapshot Snapshot, request PermitRequest) (*Proof, error) {
	return v.VerifyAndIssue(snapshot, request)
}

// Merge is an alias for Verify.
func (v *Verifier) Merge(snapshot Snapshot) (*Proof, error) {
	return v.Verify(snapshot)
}

// Check validates a snapshot without retaining an issuance record.
func (v *Verifier) Check(snapshot Snapshot) error {
	_, err := BuildProof(snapshot)
	return err
}

// Reset removes all remembered locks.  It is intended for tests and explicit
// operator resets, not for normal replay protection.
func (v *Verifier) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.issued = make(map[string]issuedRecord)
}

func verifyAndIssue(snapshot Snapshot, request PermitRequest) (*Proof, error) {
	return buildProof(snapshot, &request)
}

func normalizeRequestSnapshot(snapshot Snapshot, request PermitRequest) (*normalizedSnapshot, error) {
	snapshot.Request = &request
	snapshot.Pending = nil
	return normalizeSnapshot(snapshot)
}

func (v *Verifier) remember(snapshotDigest, requestDigest string, proof *Proof) (*Proof, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.issued == nil {
		v.issued = make(map[string]issuedRecord)
	}
	if existing, ok := v.issued[snapshotDigest]; ok {
		if existing.requestDigest != requestDigest {
			return nil, ErrSnapshotConflict
		}
		return existing.proof, nil
	}
	v.issued[snapshotDigest] = issuedRecord{
		requestDigest: requestDigest,
		proof:         cloneProof(proof),
	}
	return proof, nil
}

func cloneProof(proof *Proof) *Proof {
	if proof == nil {
		return nil
	}
	clone := *proof
	clone.Request = cloneRequest(proof.Request)
	clone.Assignment = append([]BatchAssignment(nil), proof.Assignment...)
	for i := range clone.Assignment {
		clone.Assignment[i].Credentials = cloneRefs(proof.Assignment[i].Credentials)
		clone.Assignment[i].TransactionDigests = append([]string(nil), proof.Assignment[i].TransactionDigests...)
	}
	clone.Consumed = cloneRefs(proof.Consumed)
	clone.Reserved = cloneRefs(proof.Reserved)
	clone.Locked = cloneRefs(proof.Locked)
	clone.Unavoidable = cloneRefs(proof.Unavoidable)
	clone.ConditionallyUsed = cloneRefs(proof.ConditionallyUsed)
	clone.UniversallyFree = cloneRefs(proof.UniversallyFree)
	clone.Available = cloneRefs(proof.Available)
	clone.Statuses = append([]CredentialStatus(nil), proof.Statuses...)
	for i := range clone.Statuses {
		clone.Statuses[i].Credential = cloneRef(proof.Statuses[i].Credential)
	}
	if proof.Permit != nil {
		permit := *proof.Permit
		permit.Credential = cloneRef(proof.Permit.Credential)
		clone.Permit = &permit
	}
	return &clone
}

func cloneRef(ref CredentialRef) CredentialRef {
	ref.Purposes = append([]string(nil), ref.Purposes...)
	ref.AllowedPurposes = append([]string(nil), ref.AllowedPurposes...)
	return ref
}

func cloneRefs(refs []CredentialRef) []CredentialRef {
	result := make([]CredentialRef, len(refs))
	for i, ref := range refs {
		result[i] = cloneRef(ref)
	}
	return result
}
