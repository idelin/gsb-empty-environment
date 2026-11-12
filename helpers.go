package netsec

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
)

func randomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("netsec: random material generation failed: %w", err)
	}
	return buf, nil
}

func validateLeaseRef(id, sessionID string) error {
	if id == "" {
		return errors.New("netsec: secret id is required")
	}
	if sessionID == "" {
		return errors.New("netsec: session id is required")
	}
	return nil
}

func leaseMatches(rec *record, sessionID string, nonce []byte) bool {
	return rec.sessionID == sessionID && bytes.Equal(rec.nonce, nonce)
}

func (s *Service) unknownError(rec *record) error {
	return fmt.Errorf("%w: reason=%s", ErrOutcomeUnknown, rec.reason)
}

func (s *Service) appendEvent(ev Event) {
	s.audit = append(s.audit, ev)
}

func (s *Service) leaseDTO(rec *record, payload []byte) *Lease {
	return &Lease{
		SecretID:  rec.id,
		SessionID: rec.sessionID,
		Nonce:     cloneBytes(rec.nonce),
		Payload:   cloneBytes(payload),
		ProofKey:  cloneBytes(rec.proofKey),
		ExpiresAt: rec.expiresAt,
		Attempt:   rec.attempts,
	}
}
