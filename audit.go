package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const zeroAuditHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditAction identifies a security-relevant event in the ledger.
type AuditAction string

const (
	AuditAuthorizationCreated AuditAction = "authorization_created"
	AuditAuthorizationRevoked AuditAction = "authorization_revoked"
	AuditTokenIssued          AuditAction = "token_issued"
	AuditTokenIssueDenied     AuditAction = "token_issue_denied"
	AuditTokenRevoked         AuditAction = "token_revoked"
	AuditQueryAllowed         AuditAction = "query_allowed"
	AuditQueryDenied          AuditAction = "query_denied"
)

// AuditEntry is one link in the append-only query ledger. TokenID is a
// non-secret fingerprint; raw authorization tokens are never logged.
type AuditEntry struct {
	Sequence             uint64      `json:"sequence"`
	Timestamp            time.Time   `json:"timestamp"`
	Action               AuditAction `json:"action"`
	Allowed              bool        `json:"allowed"`
	RequesterID          string      `json:"requester_id,omitempty"`
	SubjectID            string      `json:"subject_id,omitempty"`
	Purpose              string      `json:"purpose,omitempty"`
	InstitutionID        string      `json:"institution_id,omitempty"`
	AuthorizationID      string      `json:"authorization_id,omitempty"`
	TokenID              string      `json:"token_id,omitempty"`
	RequestedRequesterID string      `json:"requested_requester_id,omitempty"`
	RequestedSubjectID   string      `json:"requested_subject_id,omitempty"`
	RequestedPurpose     string      `json:"requested_purpose,omitempty"`
	RequestedInstitution string      `json:"requested_institution_id,omitempty"`
	Reason               string      `json:"reason,omitempty"`
	PreviousHash         string      `json:"previous_hash"`
	Hash                 string      `json:"hash"`
}

type auditHashInput struct {
	Sequence             uint64      `json:"sequence"`
	Timestamp            time.Time   `json:"timestamp"`
	Action               AuditAction `json:"action"`
	Allowed              bool        `json:"allowed"`
	RequesterID          string      `json:"requester_id,omitempty"`
	SubjectID            string      `json:"subject_id,omitempty"`
	Purpose              string      `json:"purpose,omitempty"`
	InstitutionID        string      `json:"institution_id,omitempty"`
	AuthorizationID      string      `json:"authorization_id,omitempty"`
	TokenID              string      `json:"token_id,omitempty"`
	RequestedRequesterID string      `json:"requested_requester_id,omitempty"`
	RequestedSubjectID   string      `json:"requested_subject_id,omitempty"`
	RequestedPurpose     string      `json:"requested_purpose,omitempty"`
	RequestedInstitution string      `json:"requested_institution_id,omitempty"`
	Reason               string      `json:"reason,omitempty"`
	PreviousHash         string      `json:"previous_hash"`
}

func (m *Manager) appendQueryAuditLocked(
	now time.Time,
	auth *Authorization,
	tokenID string,
	req QueryRequest,
	queryErr error,
) {
	entry := AuditEntry{
		Timestamp:            now,
		Action:               AuditQueryAllowed,
		Allowed:              queryErr == nil,
		RequestedRequesterID: req.RequesterID,
		RequestedSubjectID:   req.SubjectID,
		RequestedPurpose:     req.Purpose,
		RequestedInstitution: req.InstitutionID,
		TokenID:              tokenID,
	}
	if auth != nil {
		entry.AuthorizationID = auth.ID
		entry.SubjectID = auth.SubjectID
		entry.RequesterID = auth.RequesterID
		entry.Purpose = auth.Purpose
		entry.InstitutionID = auth.InstitutionID
	}
	if queryErr != nil {
		entry.Action = AuditQueryDenied
		entry.Reason = queryErr.Error()
	}
	m.appendAuditLocked(entry)
}

func (m *Manager) appendAuditLocked(entry AuditEntry) {
	entry.Sequence = uint64(len(m.audit) + 1)
	entry.PreviousHash = m.auditHash
	entry.Hash = hashAuditEntry(entry)
	m.audit = append(m.audit, entry)
	m.auditHash = entry.Hash
}

// VerifyAuditLog recomputes the complete hash chain. It returns
// ErrAuditTampered if any record or linkage has been changed.
func (m *Manager) VerifyAuditLog() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	previous := zeroAuditHash
	storedHash := m.auditHash
	if storedHash == "" {
		storedHash = zeroAuditHash
	}
	for i, entry := range m.audit {
		if entry.Sequence != uint64(i+1) || entry.PreviousHash != previous {
			return fmt.Errorf("%w at sequence %d", ErrAuditTampered, i+1)
		}
		if entry.Hash != hashAuditEntry(entry) {
			return fmt.Errorf("%w at sequence %d", ErrAuditTampered, i+1)
		}
		previous = entry.Hash
	}
	if previous != storedHash {
		return ErrAuditTampered
	}
	return nil
}

func hashAuditEntry(entry AuditEntry) string {
	input := auditHashInput{
		Sequence:             entry.Sequence,
		Timestamp:            entry.Timestamp.UTC(),
		Action:               entry.Action,
		Allowed:              entry.Allowed,
		RequesterID:          entry.RequesterID,
		SubjectID:            entry.SubjectID,
		Purpose:              entry.Purpose,
		InstitutionID:        entry.InstitutionID,
		AuthorizationID:      entry.AuthorizationID,
		TokenID:              entry.TokenID,
		RequestedRequesterID: entry.RequestedRequesterID,
		RequestedSubjectID:   entry.RequestedSubjectID,
		RequestedPurpose:     entry.RequestedPurpose,
		RequestedInstitution: entry.RequestedInstitution,
		Reason:               entry.Reason,
		PreviousHash:         entry.PreviousHash,
	}
	encoded, _ := json.Marshal(input)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
