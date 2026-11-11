package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Reserve binds label to digest under requestID before the upload completes.
//
// Retrying with the same request ID is idempotent: the exact recorded outcome
// is replayed without mutating the record. A different request ID or digest
// collides with an in-flight reservation (or with an already published
// immutable label) and fails with ErrConflict. Expired reservations are
// atomically revoked inside the same transaction; a new request ID may
// re-acquire the label while the original request ID receives
// ErrReservationExpired so it can abandon its stale attempt.
func (s *Service) Reserve(ctx context.Context, label, digest, requestID string) (*Result, error) {
	if err := validateRequest(label, digest, requestID); err != nil {
		return nil, err
	}

	var replayed bool
	rec, err := s.store.Update(ctx, label, func(cur *LabelRecord) (*LabelRecord, error) {
		switch {
		case cur == nil || cur.State == StateAbsent:
			return s.newReservation(label, digest, requestID), nil
		case cur.RequestID == requestID:
			if cur.State == StateRevoked || (cur.State == StateReserved && s.isExpired(cur)) {
				// This request's reservation already died, even if a fresh
				// request has since taken over the label.
				return nil, ErrReservationExpired
			}
			if cur.Digest != digest {
				return nil, retryDigestConflict(cur.Digest)
			}
			replayed = true
			return cur, nil
		case cur.State == StateRevoked,
			cur.State == StateReserved && s.isExpired(cur):
			// A revoked or expired reservation can be taken over only by a
			// fresh request ID.
			return s.newReservation(label, digest, requestID), nil
		default:
			return nil, ErrConflict
		}
	})
	if err != nil {
		return nil, err
	}
	return &Result{Record: rec, Replayed: replayed}, nil
}

// Publish validates the upload and transitions the label to StatePublished.
//
// The uploaded content is re-verified on every call, including idempotent
// retries, and verification happens inside the store transaction so a failed
// publish never leaves a partial effect. The same request ID with the same
// digest retries idempotently; once published, a different digest or request
// ID fails with ErrConflict. A label that has never been reserved is reserved
// and published atomically for single-shot hosts.
func (s *Service) Publish(ctx context.Context, label, digest, requestID string) (*Result, error) {
	if err := validateRequest(label, digest, requestID); err != nil {
		return nil, err
	}

	var replayed bool
	rec, err := s.store.Update(ctx, label, func(cur *LabelRecord) (*LabelRecord, error) {
		switch {
		case cur == nil || cur.State == StateAbsent:
			if err := s.reverify(ctx, label, digest); err != nil {
				return nil, err
			}
			return s.publishedRecord(label, digest, requestID), nil
		case cur.State == StatePublished:
			if cur.RequestID == requestID && cur.Digest == digest {
				if err := s.reverify(ctx, label, digest); err != nil {
					return nil, err
				}
				replayed = true
				return cur, nil
			}
			return nil, ErrConflict
		case cur.State == StateReserved && s.isExpired(cur):
			if cur.RequestID == requestID {
				return nil, ErrReservationExpired
			}
			if err := s.reverify(ctx, label, digest); err != nil {
				return nil, err
			}
			return s.publishedRecord(label, digest, requestID), nil
		case cur.State == StateReserved:
			if cur.RequestID != requestID {
				return nil, ErrConflict
			}
			if cur.Digest != digest {
				return nil, retryDigestConflict(cur.Digest)
			}
			if err := s.reverify(ctx, label, digest); err != nil {
				return nil, err
			}
			next := *cur
			next.State = StatePublished
			next.ExpiresAt = zeroTime()
			return &next, nil
		case cur.State == StateRevoked:
			if cur.RequestID == requestID {
				return nil, ErrReservationExpired
			}
			if err := s.reverify(ctx, label, digest); err != nil {
				return nil, err
			}
			return s.publishedRecord(label, digest, requestID), nil
		default:
			return nil, ErrIllegalTransition
		}
	})
	if err != nil {
		return nil, err
	}
	if rec.State != StatePublished {
		// Defensive: a storage implementation must never surface this.
		return nil, ErrIllegalTransition
	}
	return &Result{Record: rec, Replayed: replayed}, nil
}

// Release abandons a reservation. Published labels are immutable and cannot be
// released; the same request ID replaying Release is idempotent.
func (s *Service) Release(ctx context.Context, label, requestID string) (*Result, error) {
	if label == "" || requestID == "" {
		return nil, fmt.Errorf("%w: label and requestID are required", ErrInvalidArgument)
	}

	var replayed bool
	rec, err := s.store.Update(ctx, label, func(cur *LabelRecord) (*LabelRecord, error) {
		switch {
		case cur == nil || cur.State == StateAbsent:
			return nil, ErrConflict
		case cur.State == StatePublished:
			return nil, ErrConflict
		case cur.RequestID != requestID:
			return nil, ErrConflict
		case cur.State == StateRevoked:
			replayed = true
			return cur, nil
		case cur.State == StateReserved && s.isExpired(cur):
			replayed = true
			fallthrough
		default:
			next := *cur
			next.State = StateRevoked
			next.ExpiresAt = zeroTime()
			return &next, nil
		}
	})
	if err != nil {
		return nil, err
	}
	return &Result{Record: rec, Replayed: replayed}, nil
}

func (s *Service) isExpired(rec *LabelRecord) bool {
	return !rec.ExpiresAt.IsZero() && !s.now().Before(rec.ExpiresAt)
}

func (s *Service) newReservation(label, digest, requestID string) *LabelRecord {
	return &LabelRecord{
		Label:     label,
		State:     StateReserved,
		Digest:    digest,
		RequestID: requestID,
		ExpiresAt: s.now().Add(s.ttl),
	}
}

func (s *Service) publishedRecord(label, digest, requestID string) *LabelRecord {
	return &LabelRecord{
		Label:     label,
		State:     StatePublished,
		Digest:    digest,
		RequestID: requestID,
	}
}

func (s *Service) reverify(ctx context.Context, label, digest string) error {
	err := s.verifier.Verify(ctx, label, digest)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrContentNotFound), errors.Is(err, ErrContentMismatch):
		return err
	default:
		return fmt.Errorf("verify upload for %q: %w", label, err)
	}
}

func validateRequest(label, digest, requestID string) error {
	if label == "" || digest == "" || requestID == "" {
		return fmt.Errorf("%w: label, digest and requestID are required", ErrInvalidArgument)
	}
	return nil
}

func retryDigestConflict(recordedDigest string) error {
	return fmt.Errorf("%w: retry digest differs from recorded digest %q", ErrConflict, recordedDigest)
}
