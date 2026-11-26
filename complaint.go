package threshold

import (
	"bytes"
	"errors"
	"fmt"
)

// Complaint is self-contained evidence that a dealer's point-to-point share is
// inconsistent with its public Feldman vector. Any third party holding the
// Group can verify it without trusting the coordinator.
type Complaint struct {
	Context      []byte   `json:"context"`
	Dealer       ID       `json:"dealer"`
	Accuser      ID       `json:"accuser"`
	AccuserIndex int      `json:"accuser_index"`
	Share        []byte   `json:"share"`
	Commitments  [][]byte `json:"commitments"`
}

// VerifyComplaint returns nil exactly when the enclosed share fails Feldman
// verification and therefore incriminates the dealer. A well-formed valid
// share means the accusation is false and ErrComplaintUnsubstantiated.
func VerifyComplaint(g Group, complaint Complaint) error {
	if g == nil {
		return ErrGroupRequired
	}
	if complaint.Dealer == "" || complaint.Accuser == "" {
		return fmt.Errorf("%w: missing dealer or accuser", ErrInvalidComplaint)
	}
	if complaint.AccuserIndex == 0 {
		return fmt.Errorf("%w: missing accuser index", ErrInvalidComplaint)
	}
	if len(complaint.Commitments) == 0 {
		return fmt.Errorf("%w: missing commitments", ErrInvalidComplaint)
	}

	err := VerifyShare(g, complaint.Commitments, complaint.AccuserIndex, complaint.Share)
	if err == nil {
		return fmt.Errorf("%w: dealer=%q accuser=%q", ErrComplaintUnsubstantiated, complaint.Dealer, complaint.Accuser)
	}
	if errors.Is(err, ErrInvalidShare) {
		return nil
	}
	return fmt.Errorf("%w: malformed evidence: %v", ErrInvalidComplaint, err)
}

// Verify is a method form of VerifyComplaint.
func (c Complaint) Verify(g Group) error {
	return VerifyComplaint(g, c)
}

func sameCommitments(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
