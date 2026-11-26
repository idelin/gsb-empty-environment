package credentialauth

import "fmt"

// CheckProof verifies that proof is the deterministic proof for snapshot.
func CheckProof(snapshot Snapshot, proof *Proof) error {
	if proof == nil {
		return fmt.Errorf("%w: nil proof", ErrProofMismatch)
	}
	if snapshot.Request == nil && snapshot.Pending == nil && proof.Request != nil {
		snapshot.Request = proof.Request
		snapshot.Pending = nil
	}
	expected, err := BuildProof(snapshot)
	if err != nil {
		return err
	}
	if proof.Version != expected.Version ||
		proof.Digest != expected.Digest ||
		proof.SnapshotDigest != expected.SnapshotDigest ||
		proof.RequestDigest != expected.RequestDigest ||
		proof.Safety != expected.Safety ||
		proof.AssignmentDigest != expected.AssignmentDigest {
		return fmt.Errorf("%w: snapshot, request, or assignment digest differs", ErrProofMismatch)
	}
	actualAssignmentDigest, err := digestJSON(proof.Assignment)
	if err != nil || actualAssignmentDigest != proof.AssignmentDigest {
		return fmt.Errorf("%w: assignment contents do not match assignment digest", ErrProofMismatch)
	}
	if computeProofDigest(proof) != proof.Digest {
		return fmt.Errorf("%w: proof digest is invalid", ErrProofMismatch)
	}
	lockedDigest, err := digestJSON(proof.Locked)
	if err != nil {
		return fmt.Errorf("%w: cannot hash locked credentials", ErrProofMismatch)
	}
	expectedLockedDigest, err := digestJSON(expected.Locked)
	if err != nil || lockedDigest != expectedLockedDigest {
		return fmt.Errorf("%w: locked credential set differs", ErrProofMismatch)
	}
	if (proof.Permit == nil) != (expected.Permit == nil) {
		return fmt.Errorf("%w: permit presence differs", ErrProofMismatch)
	}
	if proof.Permit != nil {
		permitCredentialDigest, err := digestJSON(proof.Permit.Credential)
		if err != nil {
			return fmt.Errorf("%w: cannot hash permit credential", ErrProofMismatch)
		}
		expectedPermitCredentialDigest, err := digestJSON(expected.Permit.Credential)
		if err != nil || permitCredentialDigest != expectedPermitCredentialDigest {
			return fmt.Errorf("%w: permit credential differs", ErrProofMismatch)
		}
		if proof.Permit.LockID != expected.Permit.LockID ||
			proof.Permit.Credential.CredentialIDValue() != expected.Permit.Credential.CredentialIDValue() ||
			proof.Permit.RPID != expected.Permit.RPID ||
			proof.Permit.SnapshotDigest != expected.Permit.SnapshotDigest ||
			proof.Permit.RequestDigest != expected.Permit.RequestDigest ||
			proof.Permit.AssignmentDigest != expected.Permit.AssignmentDigest ||
			proof.Permit.TransactionDigest != expected.Permit.TransactionDigest ||
			proof.Permit.Purpose != expected.Permit.Purpose {
			return fmt.Errorf("%w: permit differs", ErrProofMismatch)
		}
	}
	return nil
}

// ValidateProof is an alias for CheckProof.
func ValidateProof(snapshot Snapshot, proof *Proof) error {
	return CheckProof(snapshot, proof)
}

// Verify checks that p is the deterministic proof for snapshot.
func (p *Proof) Verify(snapshot Snapshot) error {
	return CheckProof(snapshot, p)
}

// Valid reports whether p is the deterministic proof for snapshot.
func (p *Proof) Valid(snapshot Snapshot) bool {
	return CheckProof(snapshot, p) == nil
}
