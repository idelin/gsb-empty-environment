package writeoff

import (
	"context"
	"strings"
	"time"
)

// Service is the importable reconciliation write-off component. It is safe for
// concurrent use because every state transition runs through Store.Mutate.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService builds a Service backed by the given Store.
func NewService(store Store) *Service {
	return &Service{
		store: store,
		now:   time.Now,
	}
}

// Register records an open payment. Registering an existing transaction number
// fails with ErrPaymentExists.
func (s *Service) Register(ctx context.Context, payment Payment) (Aggregate, error) {
	if err := validatePayment(payment); err != nil {
		return Aggregate{}, err
	}
	if err := ctx.Err(); err != nil {
		return Aggregate{}, err
	}

	return s.store.Mutate(ctx, payment.TxnID, func(agg Aggregate) (Aggregate, error) {
		if agg.Status != StatusOpen || agg.Payment.TxnID != "" {
			return Aggregate{}, ErrPaymentExists
		}
		now := s.now().UTC()
		agg.Payment = payment
		agg.Status = StatusOpen
		agg.CreatedAt = now
		agg.UpdatedAt = now
		return agg, nil
	})
}

// Get returns a copy of the current aggregate for txnID.
func (s *Service) Get(ctx context.Context, txnID string) (Aggregate, error) {
	if err := ctx.Err(); err != nil {
		return Aggregate{}, err
	}
	return s.store.Load(ctx, txnID)
}

// Confirm applies a write-off confirmation.
//
// The confirmation must match the payment in transaction number, amount,
// currency and accounting period. Cross-period confirmations, currency
// mismatches and partial amounts are rejected. A valid confirmation moves the
// payment from open to written-off once; a later confirmation for the same
// payment is rejected. Re-sending the same ConfirmID replays the original
// result with Replayed set to true and never changes the ledger again.
func (s *Service) Confirm(ctx context.Context, confirm Confirmation) (ConfirmResult, error) {
	if err := validateConfirmation(confirm); err != nil {
		return ConfirmResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ConfirmResult{}, err
	}

	var result ConfirmResult
	_, err := s.store.Mutate(ctx, confirm.TxnID, func(agg Aggregate) (Aggregate, error) {
		if agg.Payment.TxnID == "" {
			return Aggregate{}, ErrPaymentNotFound
		}

		// Idempotency wins over every other check: a replay always returns the
		// original outcome even when the payment has since been reversed.
		if agg.Confirm != nil && agg.Confirm.ConfirmID == confirm.ConfirmID {
			if !sameConfirmation(*agg.Confirm, confirm) {
				return Aggregate{}, ErrConflict
			}
			result = ConfirmResult{
				TxnID:     agg.Payment.TxnID,
				ConfirmID: confirm.ConfirmID,
				Status:    StatusWrittenOff,
				Replayed:  true,
			}
			return agg, nil
		}

		switch agg.Status {
		case StatusReversed:
			return Aggregate{}, ErrAlreadyReversed
		case StatusWrittenOff:
			return Aggregate{}, ErrAlreadyWrittenOff
		case StatusOpen:
		default:
			return Aggregate{}, ErrInvalidState
		}

		if err := matchPayment(agg.Payment, confirm.Period, confirm.Currency, confirm.Amount); err != nil {
			return Aggregate{}, err
		}

		stored := confirm
		now := s.now().UTC()
		agg.Status = StatusWrittenOff
		agg.Confirm = &stored
		agg.WrittenOffAt = now
		agg.UpdatedAt = now

		result = ConfirmResult{
			TxnID:     agg.Payment.TxnID,
			ConfirmID: confirm.ConfirmID,
			Status:    StatusWrittenOff,
			Replayed:  false,
		}
		return agg, nil
	})
	if err != nil {
		return ConfirmResult{}, err
	}
	return result, nil
}

// Reverse cancels a previously applied write-off. Reversal is a forward-only
// transition from written-off to reversed and can never move a payment back to
// open. The reversal must match the payment in amount, currency and accounting
// period. Re-sending the same ReversalID replays the original result.
func (s *Service) Reverse(ctx context.Context, reversal Reversal) (ReversalResult, error) {
	if err := validateReversal(reversal); err != nil {
		return ReversalResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReversalResult{}, err
	}

	var result ReversalResult
	_, err := s.store.Mutate(ctx, reversal.TxnID, func(agg Aggregate) (Aggregate, error) {
		if agg.Payment.TxnID == "" {
			return Aggregate{}, ErrPaymentNotFound
		}

		if agg.Reversal != nil && agg.Reversal.ReversalID == reversal.ReversalID {
			if !sameReversal(*agg.Reversal, reversal) {
				return Aggregate{}, ErrConflict
			}
			result = ReversalResult{
				TxnID:      agg.Payment.TxnID,
				ReversalID: reversal.ReversalID,
				Status:     StatusReversed,
				Replayed:   true,
			}
			return agg, nil
		}

		switch agg.Status {
		case StatusReversed:
			return Aggregate{}, ErrAlreadyReversed
		case StatusOpen:
			return Aggregate{}, ErrInvalidState
		case StatusWrittenOff:
		default:
			return Aggregate{}, ErrInvalidState
		}

		if err := matchPayment(agg.Payment, reversal.Period, reversal.Currency, reversal.Amount); err != nil {
			return Aggregate{}, err
		}

		stored := reversal
		now := s.now().UTC()
		agg.Status = StatusReversed
		agg.Reversal = &stored
		agg.ReversedAt = now
		agg.UpdatedAt = now

		result = ReversalResult{
			TxnID:      agg.Payment.TxnID,
			ReversalID: reversal.ReversalID,
			Status:     StatusReversed,
			Replayed:   false,
		}
		return agg, nil
	})
	if err != nil {
		return ReversalResult{}, err
	}
	return result, nil
}

// validatePayment checks registration input. Values are trimmed of surrounding
// whitespace for the well-formedness check only; matching uses the raw values.
func validatePayment(p Payment) error {
	if strings.TrimSpace(p.TxnID) == "" ||
		strings.TrimSpace(p.Currency) == "" ||
		strings.TrimSpace(p.Period) == "" ||
		p.Amount <= 0 {
		return ErrInvalidPayment
	}
	return nil
}

// validateConfirmation checks confirmation input.
func validateConfirmation(c Confirmation) error {
	if strings.TrimSpace(c.ConfirmID) == "" ||
		strings.TrimSpace(c.TxnID) == "" ||
		strings.TrimSpace(c.Currency) == "" ||
		strings.TrimSpace(c.Period) == "" ||
		c.Amount <= 0 {
		return ErrInvalidConfirmation
	}
	return nil
}

// validateReversal checks reversal input.
func validateReversal(r Reversal) error {
	if strings.TrimSpace(r.ReversalID) == "" ||
		strings.TrimSpace(r.TxnID) == "" ||
		strings.TrimSpace(r.Currency) == "" ||
		strings.TrimSpace(r.Period) == "" ||
		r.Amount <= 0 {
		return ErrInvalidReversal
	}
	return nil
}

// matchPayment enforces the four-way binding between an instruction and the
// payment. The accounting period is checked first because crossing periods is
// the hard reconciliation boundary; partial amounts are reported distinctly.
func matchPayment(p Payment, period, currency string, amount int64) error {
	if period != p.Period {
		return ErrPeriodMismatch
	}
	if currency != p.Currency {
		return ErrCurrencyMismatch
	}
	if amount < p.Amount {
		return ErrPartialAmount
	}
	if amount > p.Amount {
		return ErrAmountMismatch
	}
	return nil
}

// sameConfirmation reports whether two confirmations with the same idempotency
// key carry identical business payloads.
func sameConfirmation(a, b Confirmation) bool {
	return a.TxnID == b.TxnID &&
		a.Amount == b.Amount &&
		a.Currency == b.Currency &&
		a.Period == b.Period
}

// sameReversal reports whether two reversals with the same idempotency key
// carry identical business payloads.
func sameReversal(a, b Reversal) bool {
	return a.TxnID == b.TxnID &&
		a.Amount == b.Amount &&
		a.Currency == b.Currency &&
		a.Period == b.Period
}
