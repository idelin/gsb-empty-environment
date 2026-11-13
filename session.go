package netsec

import (
	"fmt"
	"sync"
	"time"
)

// Session is a two-client view of one account pair. It is deliberately strict:
// Verified is possible only when both clients hold the same complete device
// sets for both accounts and either a QR verification or user confirmation
// binds that exact state.
type Session struct {
	A *Client
	B *Client

	mu           sync.RWMutex
	baseline     *verificationBinding
	confirmation *ConfirmationRecord
	lastResult   *VerificationResult
}

// NewSession creates a pair session for two distinct accounts.
func NewSession(a, b *Client) (*Session, error) {
	if a == nil || b == nil {
		return nil, fmt.Errorf("netsec: nil session client")
	}
	if a.AccountID() == "" || b.AccountID() == "" {
		return nil, ErrInvalidAccountID
	}
	if a.AccountID() == b.AccountID() {
		return nil, ErrDuplicateAccount
	}
	return &Session{A: a, B: b}, nil
}

// AccountIDs returns the two account IDs in session order.
func (s *Session) AccountIDs() (string, string) {
	if s == nil || s.A == nil || s.B == nil {
		return "", ""
	}
	return s.A.AccountID(), s.B.AccountID()
}

// ViewsConsistent reports whether both clients hold the same complete device
// identity set, at the same list version, for both accounts. Online presence
// is ignored.
func (s *Session) ViewsConsistent() bool {
	if s == nil || s.A == nil || s.B == nil {
		return false
	}
	for _, accountID := range []string{s.A.AccountID(), s.B.AccountID()} {
		left, ok := s.A.SnapshotFor(accountID)
		if !ok {
			return false
		}
		right, ok := s.B.SnapshotFor(accountID)
		if !ok {
			return false
		}
		if !left.SameDeviceSet(right) {
			return false
		}
	}
	return true
}

// CompleteDeviceSetsMatch is an alias for ViewsConsistent.
func (s *Session) CompleteDeviceSetsMatch() bool {
	return s.ViewsConsistent()
}

// SafetyNumberFrom computes the safety number from one client's local view.
func (s *Session) SafetyNumberFrom(client *Client) (*SafetyNumber, error) {
	if s == nil || client == nil {
		return nil, fmt.Errorf("netsec: nil session/client")
	}
	if client.AccountID() == s.A.AccountID() {
		return client.SafetyNumberFor(s.B.AccountID())
	}
	if client.AccountID() == s.B.AccountID() {
		return client.SafetyNumberFor(s.A.AccountID())
	}
	return nil, fmt.Errorf("netsec: client is not part of session")
}

// SafetyNumbers returns the safety number computed independently from each
// client's local view. They should be identical when complete device sets
// agree; a mismatch is the multi-device attack signal.
func (s *Session) SafetyNumbers() (*SafetyNumber, *SafetyNumber, error) {
	if s == nil || s.A == nil || s.B == nil {
		return nil, nil, fmt.Errorf("netsec: nil session client")
	}
	left, err := s.SafetyNumberFrom(s.A)
	if err != nil {
		return nil, nil, err
	}
	right, err := s.SafetyNumberFrom(s.B)
	if err != nil {
		return nil, nil, err
	}
	return left, right, nil
}

// SafetyNumber returns the common safety number. It refuses to return one
// when the two clients' complete device sets differ.
func (s *Session) SafetyNumber() (*SafetyNumber, error) {
	if s == nil || !s.ViewsConsistent() {
		return nil, ErrViewsMismatch
	}
	left, right, err := s.SafetyNumbers()
	if err != nil {
		return nil, err
	}
	if left.Checksum != right.Checksum {
		return nil, ErrViewsMismatch
	}
	return left, nil
}

// QRPayloadFrom generates a payload from one client's local view. This is
// useful for testing two clients that hold different snapshots.
func (s *Session) QRPayloadFrom(client *Client) (*QRPayload, error) {
	if s == nil || client == nil {
		return nil, fmt.Errorf("netsec: nil session/client")
	}
	if client.AccountID() == s.A.AccountID() {
		return client.GenerateQRPayload(s.B.AccountID())
	}
	if client.AccountID() == s.B.AccountID() {
		return client.GenerateQRPayload(s.A.AccountID())
	}
	return nil, fmt.Errorf("netsec: client is not part of session")
}

// QRPayload generates the common payload only when both client views agree.
func (s *Session) QRPayload() (*QRPayload, error) {
	if s == nil || !s.ViewsConsistent() {
		return nil, ErrViewsMismatch
	}
	return s.QRPayloadFrom(s.A)
}

// VerifyQRPayload checks a payload against both clients' complete current
// views. Both must display Verified for the session to do so.
func (s *Session) VerifyQRPayload(payload *QRPayload) (*VerificationResult, error) {
	if s == nil || s.A == nil || s.B == nil {
		return invalidResult(payload, "nil session/client"), fmt.Errorf("netsec: nil session client")
	}
	left, leftErr := s.A.VerifyQRPayload(payload, s.B.AccountID())
	right, rightErr := s.B.VerifyQRPayload(payload, s.A.AccountID())

	result := combineSessionResults(payload, left, right, leftErr, rightErr)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResult = result
	if result.Status == StatusVerified {
		ownA, okA := s.A.SnapshotFor(s.A.AccountID())
		peerA, okB := s.A.SnapshotFor(s.B.AccountID())
		if okA && okB {
			if binding, err := bindingFromSnapshots(ownA, peerA); err == nil {
				s.baseline = &binding
			}
		}
	}
	return result, combineError(leftErr, rightErr, result)
}

// VerifyQR decodes and checks QR payload bytes against both client views.
func (s *Session) VerifyQR(encoded []byte) (*VerificationResult, error) {
	payload, err := DecodeQRPayload(encoded)
	if err != nil {
		result := invalidResult(nil, err.Error())
		if s != nil {
			s.mu.Lock()
			s.lastResult = result
			s.mu.Unlock()
		}
		return result, err
	}
	return s.VerifyQRPayload(payload)
}

// VerifyQRCode is an alias for VerifyQR.
func (s *Session) VerifyQRCode(encoded []byte) (*VerificationResult, error) {
	return s.VerifyQR(encoded)
}

// Status reports the current display state for the pair.
func (s *Session) Status() VerificationStatus {
	if s == nil || s.A == nil || s.B == nil {
		return StatusUnverified
	}
	if !s.ViewsConsistent() {
		return s.viewMismatchStatus()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastResult != nil && s.lastResult.Status != StatusVerified {
		return s.lastResult.Status
	}
	if s.baseline != nil {
		if s.bindingMatchesCurrentLocked(*s.baseline) {
			return StatusVerified
		}
		return StatusStale
	}
	if s.confirmation != nil {
		if !s.confirmation.Revoked && s.bindingMatchesCurrentLocked(s.confirmation.Binding()) {
			return StatusVerified
		}
		return StatusStale
	}
	if s.lastResult != nil {
		return s.lastResult.Status
	}

	leftStatus := s.A.Status(s.B.AccountID())
	rightStatus := s.B.Status(s.A.AccountID())
	switch {
	case leftStatus == StatusStale || rightStatus == StatusStale:
		return StatusStale
	case leftStatus == StatusMismatch || rightStatus == StatusMismatch ||
		leftStatus == StatusInvalid || rightStatus == StatusInvalid:
		return StatusMismatch
	case leftStatus == StatusVerified || rightStatus == StatusVerified:
		return StatusVerified
	default:
		return StatusUnverified
	}
}

// VerificationStatus is an alias for Status.
func (s *Session) VerificationStatus() VerificationStatus {
	return s.Status()
}

// Confirm binds both clients' current complete views. It refuses to confirm
// when their snapshots differ.
func (s *Session) Confirm(method ConfirmationMethod, at time.Time) (*ConfirmationRecord, error) {
	if s == nil || s.A == nil || s.B == nil {
		return nil, fmt.Errorf("netsec: nil session client")
	}
	if !s.ViewsConsistent() {
		return nil, ErrViewsMismatch
	}
	own, ok := s.A.SnapshotFor(s.A.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, s.A.AccountID())
	}
	peer, ok := s.A.SnapshotFor(s.B.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, s.B.AccountID())
	}
	record, err := NewConfirmationRecord(own, peer, method, at)
	if err != nil {
		return nil, err
	}
	s.A.storeConfirmation(s.B.AccountID(), record)
	s.B.storeConfirmation(s.A.AccountID(), record)
	s.mu.Lock()
	s.confirmation = record.Clone()
	s.baseline = nil
	s.lastResult = nil
	s.mu.Unlock()
	return record.Clone(), nil
}

// ConfirmNow is Confirm at the current UTC time.
func (s *Session) ConfirmNow(method ConfirmationMethod) (*ConfirmationRecord, error) {
	return s.Confirm(method, time.Now().UTC())
}

// Confirmation returns a copy of the session confirmation, if any.
func (s *Session) Confirmation() (*ConfirmationRecord, bool) {
	if s == nil {
		return nil, false
	}
	if s.A != nil && s.B != nil {
		if record, ok := s.A.ConfirmationFor(s.B.AccountID()); ok {
			return record, true
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.confirmation == nil {
		return nil, false
	}
	return s.confirmation.Clone(), true
}

func (s *Session) viewMismatchStatus() VerificationStatus {
	newer := false
	mismatch := false
	for _, accountID := range []string{s.A.AccountID(), s.B.AccountID()} {
		left, ok := s.A.SnapshotFor(accountID)
		if !ok {
			mismatch = true
			continue
		}
		right, ok := s.B.SnapshotFor(accountID)
		if !ok {
			mismatch = true
			continue
		}
		if left.Version != right.Version {
			newer = true
			continue
		}
		if !left.SameDeviceSet(right) {
			mismatch = true
		}
	}
	if newer {
		return StatusStale
	}
	if mismatch {
		return StatusMismatch
	}
	return StatusUnverified
}

func (s *Session) bindingMatchesCurrentLocked(binding verificationBinding) bool {
	if len(binding.parties) != 2 {
		return false
	}
	for accountID, party := range binding.parties {
		snapshot, ok := s.A.SnapshotFor(accountID)
		if !ok || !partyMatchesSnapshot(party, snapshot) {
			return false
		}
	}
	return true
}

func combineSessionResults(payload *QRPayload, left, right *VerificationResult, leftErr, rightErr error) *VerificationResult {
	if left == nil {
		left = invalidResult(payload, "nil verification result")
	}
	if right == nil {
		right = invalidResult(payload, "nil verification result")
	}
	if left.Status == StatusVerified && right.Status == StatusVerified {
		return &VerificationResult{
			Status:       StatusVerified,
			Reason:       "both clients verified the complete device sets",
			Payload:      payload,
			SafetyNumber: left.SafetyNumber,
		}
	}
	if left.Status == StatusStale || right.Status == StatusStale {
		return &VerificationResult{
			Status:        StatusStale,
			Reason:        "one or both client views are stale relative to the QR payload",
			Payload:       payload,
			StaleAccounts: append(left.StaleAccounts, right.StaleAccounts...),
		}
	}
	if left.Status == StatusMismatch || right.Status == StatusMismatch {
		return &VerificationResult{
			Status:  StatusMismatch,
			Reason:  "one or both client device sets differ from the QR payload",
			Payload: payload,
		}
	}
	return invalidResult(payload, "session QR verification invalid")
}

func combineError(leftErr, rightErr error, result *VerificationResult) error {
	if result != nil && result.Status == StatusVerified {
		return nil
	}
	if leftErr != nil {
		return leftErr
	}
	return rightErr
}
