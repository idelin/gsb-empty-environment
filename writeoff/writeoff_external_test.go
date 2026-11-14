package writeoff_test

import (
	"context"
	"errors"
	"testing"

	"example.org/netsec/q0428/writeoff"
)

// TestExternalImport exercises the full public API from another package to
// guarantee the component is importable by reconciliation services.
func TestExternalImport(t *testing.T) {
	svc := writeoff.NewService(writeoff.NewMemoryStore())
	ctx := context.Background()

	payment := writeoff.Payment{TxnID: "EXT-1", Amount: 2500, Currency: "CNY", Period: "2026-11"}
	if _, err := svc.Register(ctx, payment); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	confirm := writeoff.Confirmation{
		ConfirmID: "EXT-C-1",
		TxnID:     payment.TxnID,
		Amount:    payment.Amount,
		Currency:  payment.Currency,
		Period:    payment.Period,
	}
	applied, err := svc.Confirm(ctx, confirm)
	if err != nil || applied.Replayed || applied.Status != writeoff.StatusWrittenOff {
		t.Fatalf("Confirm() = %+v, %v", applied, err)
	}

	replayed, err := svc.Confirm(ctx, confirm)
	if err != nil || !replayed.Replayed {
		t.Fatalf("replayed Confirm() = %+v, %v", replayed, err)
	}

	// Binding checks (period, amount) are exercised on a still-open payment;
	// an already written-off payment rejects new confirmations by state first.
	payment2 := writeoff.Payment{TxnID: "EXT-2", Amount: 2500, Currency: "CNY", Period: "2026-11"}
	if _, err := svc.Register(ctx, payment2); err != nil {
		t.Fatalf("Register(EXT-2) error = %v", err)
	}
	crossPeriod := confirm
	crossPeriod.TxnID = "EXT-2"
	crossPeriod.Period = "2026-12"
	if _, err := svc.Confirm(ctx, crossPeriod); !errors.Is(err, writeoff.ErrPeriodMismatch) {
		t.Fatalf("cross-period error = %v, want ErrPeriodMismatch", err)
	}

	partial := writeoff.Confirmation{
		ConfirmID: "EXT-C-3",
		TxnID:     "EXT-2",
		Amount:    payment2.Amount - 1,
		Currency:  payment2.Currency,
		Period:    payment2.Period,
	}
	if _, err := svc.Confirm(ctx, partial); !errors.Is(err, writeoff.ErrPartialAmount) {
		t.Fatalf("partial amount error = %v, want ErrPartialAmount", err)
	}

	reversal := writeoff.Reversal{
		ReversalID: "EXT-R-1",
		TxnID:      payment.TxnID,
		Amount:     payment.Amount,
		Currency:   payment.Currency,
		Period:     payment.Period,
	}
	reversed, err := svc.Reverse(ctx, reversal)
	if err != nil || reversed.Status != writeoff.StatusReversed {
		t.Fatalf("Reverse() = %+v, %v", reversed, err)
	}

	agg, err := svc.Get(ctx, payment.TxnID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if agg.Status != writeoff.StatusReversed || agg.Confirm == nil || agg.Reversal == nil {
		t.Fatalf("unexpected aggregate: %+v", agg)
	}

	if got := writeoff.StatusWrittenOff.String(); got != "written-off" {
		t.Fatalf("status string = %q", got)
	}
}
