package writeoff

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func testPayment() Payment {
	return Payment{TxnID: "TXN-1", Amount: 1000, Currency: "CNY", Period: "2026-11"}
}

func newTestService(t *testing.T) (*Service, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	svc := NewService(store)
	clock := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	return svc, store
}

func registerPayment(t *testing.T, svc *Service, payment Payment) Aggregate {
	t.Helper()
	agg, err := svc.Register(context.Background(), payment)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return agg
}

func matchingConfirmation(id string) Confirmation {
	return Confirmation{
		ConfirmID: id,
		TxnID:     "TXN-1",
		Amount:    1000,
		Currency:  "CNY",
		Period:    "2026-11",
	}
}

func matchingReversal(id string) Reversal {
	return Reversal{
		ReversalID: id,
		TxnID:      "TXN-1",
		Amount:     1000,
		Currency:   "CNY",
		Period:     "2026-11",
	}
}

func TestRegisterAndGet(t *testing.T) {
	svc, _ := newTestService(t)

	agg := registerPayment(t, svc, testPayment())
	if agg.Status != StatusOpen || agg.Confirm != nil || agg.Reversal != nil {
		t.Fatalf("unexpected initial aggregate: %+v", agg)
	}
	if agg.CreatedAt.IsZero() || agg.UpdatedAt.IsZero() {
		t.Fatal("expected timestamps to be set")
	}

	if _, err := svc.Register(context.Background(), testPayment()); !errors.Is(err, ErrPaymentExists) {
		t.Fatalf("duplicate Register error = %v, want ErrPaymentExists", err)
	}

	if _, err := svc.Get(context.Background(), "missing"); !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("Get() error = %v, want ErrPaymentNotFound", err)
	}

	bad := []Payment{
		{TxnID: " ", Amount: 1, Currency: "CNY", Period: "p"},
		{TxnID: "x", Amount: 0, Currency: "CNY", Period: "p"},
		{TxnID: "x", Amount: 1, Currency: "", Period: "p"},
		{TxnID: "x", Amount: 1, Currency: "CNY", Period: ""},
	}
	for i, payment := range bad {
		if _, err := svc.Register(context.Background(), payment); !errors.Is(err, ErrInvalidPayment) {
			t.Fatalf("bad payment #%d error = %v, want ErrInvalidPayment", i, err)
		}
	}
}

func TestLegalWriteOff(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())

	result, err := svc.Confirm(context.Background(), matchingConfirmation("C-1"))
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.Replayed || result.Status != StatusWrittenOff || result.TxnID != "TXN-1" {
		t.Fatalf("unexpected result: %+v", result)
	}

	agg, err := store.Load(context.Background(), "TXN-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if agg.Status != StatusWrittenOff || agg.Confirm == nil || agg.Confirm.ConfirmID != "C-1" {
		t.Fatalf("aggregate was not written off once: %+v", agg)
	}
	if agg.WrittenOffAt.IsZero() || !agg.ReversedAt.IsZero() {
		t.Fatalf("unexpected timestamps: %+v", agg)
	}
}

func TestDuplicateConfirmationReplaysOriginalResult(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())

	first, err := svc.Confirm(context.Background(), matchingConfirmation("C-1"))
	if err != nil {
		t.Fatalf("first Confirm() error = %v", err)
	}
	if first.Replayed {
		t.Fatal("first confirmation must not be marked as replay")
	}

	before, _ := store.Load(context.Background(), "TXN-1")
	want := ConfirmResult{TxnID: "TXN-1", ConfirmID: "C-1", Status: StatusWrittenOff, Replayed: true}
	for i := 0; i < 3; i++ {
		replay, err := svc.Confirm(context.Background(), matchingConfirmation("C-1"))
		if err != nil {
			t.Fatalf("replay #%d error = %v", i, err)
		}
		if replay != want {
			t.Fatalf("replay #%d = %+v, want %+v", i, replay, want)
		}
	}

	after, _ := store.Load(context.Background(), "TXN-1")
	if after.Status != before.Status || after.Confirm == nil || after.Confirm.ConfirmID != "C-1" {
		t.Fatal("replay mutated stored confirmation")
	}
	if !after.WrittenOffAt.Equal(before.WrittenOffAt) || after.UpdatedAt != before.UpdatedAt {
		t.Fatal("replay changed write-off timestamps")
	}
}

func TestSecondDistinctConfirmationRejected(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())

	if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-2")); !errors.Is(err, ErrAlreadyWrittenOff) {
		t.Fatalf("second confirmation error = %v, want ErrAlreadyWrittenOff", err)
	}

	agg, _ := store.Load(context.Background(), "TXN-1")
	if agg.Confirm == nil || agg.Confirm.ConfirmID != "C-1" {
		t.Fatalf("original confirmation was disturbed: %+v", agg)
	}
}

func TestConfirmationBindingRejections(t *testing.T) {
	tests := []struct {
		name    string
		confirm Confirmation
		want    error
	}{
		{
			name:    "cross period",
			confirm: Confirmation{ConfirmID: "C", TxnID: "TXN-1", Amount: 1000, Currency: "CNY", Period: "2026-12"},
			want:    ErrPeriodMismatch,
		},
		{
			name:    "currency mismatch",
			confirm: Confirmation{ConfirmID: "C", TxnID: "TXN-1", Amount: 1000, Currency: "USD", Period: "2026-11"},
			want:    ErrCurrencyMismatch,
		},
		{
			name:    "partial amount",
			confirm: Confirmation{ConfirmID: "C", TxnID: "TXN-1", Amount: 999, Currency: "CNY", Period: "2026-11"},
			want:    ErrPartialAmount,
		},
		{
			name:    "over amount",
			confirm: Confirmation{ConfirmID: "C", TxnID: "TXN-1", Amount: 1001, Currency: "CNY", Period: "2026-11"},
			want:    ErrAmountMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newTestService(t)
			registerPayment(t, svc, testPayment())

			_, err := svc.Confirm(context.Background(), tt.confirm)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Confirm() error = %v, want %v", err, tt.want)
			}

			agg, _ := store.Load(context.Background(), "TXN-1")
			if agg.Status != StatusOpen || agg.Confirm != nil {
				t.Fatalf("rejected confirmation changed state: %+v", agg)
			}
		})
	}
}

func TestConfirmationMalformedAndUnknownTxn(t *testing.T) {
	svc, _ := newTestService(t)
	registerPayment(t, svc, testPayment())

	bad := []Confirmation{
		{ConfirmID: " ", TxnID: "TXN-1", Amount: 1, Currency: "CNY", Period: "p"},
		{ConfirmID: "C", TxnID: "TXN-1", Amount: 0, Currency: "CNY", Period: "p"},
		{ConfirmID: "C", TxnID: "TXN-1", Amount: 1, Currency: " ", Period: "p"},
		{ConfirmID: "C", TxnID: "TXN-1", Amount: 1, Currency: "CNY", Period: " "},
	}
	for i, confirm := range bad {
		if _, err := svc.Confirm(context.Background(), confirm); !errors.Is(err, ErrInvalidConfirmation) {
			t.Fatalf("bad confirmation #%d error = %v, want ErrInvalidConfirmation", i, err)
		}
	}

	_, err := svc.Confirm(context.Background(), Confirmation{
		ConfirmID: "C", TxnID: "UNKNOWN", Amount: 1, Currency: "CNY", Period: "2026-11",
	})
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("unknown txn error = %v, want ErrPaymentNotFound", err)
	}
}

func TestReversalForwardOnlyAndReplay(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())

	if _, err := svc.Reverse(context.Background(), matchingReversal("R-1")); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reversing open payment error = %v, want ErrInvalidState", err)
	}

	if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}

	result, err := svc.Reverse(context.Background(), matchingReversal("R-1"))
	if err != nil {
		t.Fatalf("Reverse() error = %v", err)
	}
	if result.Replayed || result.Status != StatusReversed {
		t.Fatalf("unexpected reversal result: %+v", result)
	}

	agg, _ := store.Load(context.Background(), "TXN-1")
	if agg.Status != StatusReversed || agg.Reversal == nil || agg.ReversedAt.IsZero() {
		t.Fatalf("aggregate not reversed: %+v", agg)
	}

	replay, err := svc.Reverse(context.Background(), matchingReversal("R-1"))
	if err != nil {
		t.Fatalf("replayed Reverse() error = %v", err)
	}
	want := ReversalResult{TxnID: "TXN-1", ReversalID: "R-1", Status: StatusReversed, Replayed: true}
	if replay != want {
		t.Fatalf("reversal replay = %+v, want %+v", replay, want)
	}

	if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-2")); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("confirm after reversal error = %v, want ErrAlreadyReversed", err)
	}
	if _, err := svc.Reverse(context.Background(), matchingReversal("R-2")); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("second reversal error = %v, want ErrAlreadyReversed", err)
	}

	final, _ := store.Load(context.Background(), "TXN-1")
	if final.Status != StatusReversed {
		t.Fatalf("state must stay reversed, got %v", final.Status)
	}
}

func TestReversalBindingRejections(t *testing.T) {
	tests := []struct {
		name     string
		reversal Reversal
		want     error
	}{
		{"cross period", Reversal{ReversalID: "R", TxnID: "TXN-1", Amount: 1000, Currency: "CNY", Period: "2026-10"}, ErrPeriodMismatch},
		{"currency", Reversal{ReversalID: "R", TxnID: "TXN-1", Amount: 1000, Currency: "USD", Period: "2026-11"}, ErrCurrencyMismatch},
		{"partial", Reversal{ReversalID: "R", TxnID: "TXN-1", Amount: 500, Currency: "CNY", Period: "2026-11"}, ErrPartialAmount},
		{"over", Reversal{ReversalID: "R", TxnID: "TXN-1", Amount: 2000, Currency: "CNY", Period: "2026-11"}, ErrAmountMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newTestService(t)
			registerPayment(t, svc, testPayment())
			if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
				t.Fatalf("Confirm() error = %v", err)
			}

			if _, err := svc.Reverse(context.Background(), tt.reversal); !errors.Is(err, tt.want) {
				t.Fatalf("Reverse() error = %v, want %v", err, tt.want)
			}
			agg, _ := store.Load(context.Background(), "TXN-1")
			if agg.Status != StatusWrittenOff || agg.Reversal != nil {
				t.Fatalf("rejected reversal changed state: %+v", agg)
			}
		})
	}
}

func TestReplayAfterReversalReturnsOriginalWriteOff(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())
	if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if _, err := svc.Reverse(context.Background(), matchingReversal("R-1")); err != nil {
		t.Fatalf("Reverse() error = %v", err)
	}

	result, err := svc.Confirm(context.Background(), matchingConfirmation("C-1"))
	if err != nil {
		t.Fatalf("replayed confirm after reversal error = %v", err)
	}
	if !result.Replayed || result.Status != StatusWrittenOff {
		t.Fatalf("replay = %+v, want original written-off result", result)
	}

	agg, _ := store.Load(context.Background(), "TXN-1")
	if agg.Status != StatusReversed {
		t.Fatalf("replay must not roll state back, got %v", agg.Status)
	}
}

func TestIdempotencyKeyConflict(t *testing.T) {
	t.Run("confirmation", func(t *testing.T) {
		svc, store := newTestService(t)
		registerPayment(t, svc, testPayment())
		if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
			t.Fatalf("Confirm() error = %v", err)
		}

		conflict := matchingConfirmation("C-1")
		conflict.Amount = 999
		if _, err := svc.Confirm(context.Background(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict error = %v, want ErrConflict", err)
		}
		agg, _ := store.Load(context.Background(), "TXN-1")
		if agg.Confirm == nil || agg.Confirm.Amount != 1000 {
			t.Fatalf("conflict altered stored confirmation: %+v", agg)
		}
	})

	t.Run("reversal", func(t *testing.T) {
		svc, store := newTestService(t)
		registerPayment(t, svc, testPayment())
		if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
			t.Fatalf("Confirm() error = %v", err)
		}
		if _, err := svc.Reverse(context.Background(), matchingReversal("R-1")); err != nil {
			t.Fatalf("Reverse() error = %v", err)
		}

		conflict := matchingReversal("R-1")
		conflict.Period = "2026-12"
		if _, err := svc.Reverse(context.Background(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("reversal conflict error = %v, want ErrConflict", err)
		}
		agg, _ := store.Load(context.Background(), "TXN-1")
		if agg.Reversal == nil || agg.Reversal.Period != "2026-11" {
			t.Fatalf("conflict altered stored reversal: %+v", agg)
		}
	})
}

func TestConcurrentReplaysWriteOffExactlyOnce(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())

	const goroutines = 64
	var wg sync.WaitGroup
	results := make(chan ConfirmResult, goroutines)
	errs := make(chan error, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			result, err := svc.Confirm(context.Background(), matchingConfirmation("C-1"))
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("replayed confirmation failed: %v", err)
	}

	var applied, replayed int
	for result := range results {
		if result.Replayed {
			replayed++
		} else {
			applied++
		}
	}
	if applied != 1 || replayed != goroutines-1 {
		t.Fatalf("applied=%d replayed=%d, want exactly one applied", applied, replayed)
	}

	agg, _ := store.Load(context.Background(), "TXN-1")
	if agg.Status != StatusWrittenOff || agg.Confirm == nil || agg.Confirm.ConfirmID != "C-1" {
		t.Fatalf("aggregate not written off exactly once: %+v", agg)
	}
}

func TestConcurrentDistinctConfirmationsOnlyOneSucceeds(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())

	const goroutines = 32
	var wg sync.WaitGroup
	successes := make(chan ConfirmResult, goroutines)
	failures := make(chan error, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			result, err := svc.Confirm(context.Background(), matchingConfirmation("C-"+itoa(i)))
			if err != nil {
				failures <- err
				return
			}
			successes <- result
		}(i)
	}
	wg.Wait()
	close(successes)
	close(failures)

	for err := range failures {
		if !errors.Is(err, ErrAlreadyWrittenOff) {
			t.Fatalf("unexpected failure: %v", err)
		}
	}
	if len(successes) != 1 {
		t.Fatalf("successes=%d, want 1", len(successes))
	}

	agg, _ := store.Load(context.Background(), "TXN-1")
	if agg.Status != StatusWrittenOff {
		t.Fatalf("unexpected status %v", agg.Status)
	}
}

func TestMemoryStoreIsolatesSnapshots(t *testing.T) {
	svc, store := newTestService(t)
	registerPayment(t, svc, testPayment())
	if _, err := svc.Confirm(context.Background(), matchingConfirmation("C-1")); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}

	first, err := store.Load(context.Background(), "TXN-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	first.Status = StatusReversed
	first.Confirm.Amount = 1

	second, err := store.Load(context.Background(), "TXN-1")
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if second.Status != StatusWrittenOff || second.Confirm.Amount != 1000 {
		t.Fatalf("store returned mutable internal state: %+v", second)
	}

	listed, err := store.List(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("List() = %v, %v", listed, err)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusOpen:       "open",
		StatusWrittenOff: "written-off",
		StatusReversed:   "reversed",
		Status(99):       "unknown",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("Status(%d).String() = %q, want %q", status, got, want)
		}
	}
}

func TestInvalidReversalAndUnknownReversalTxn(t *testing.T) {
	svc, _ := newTestService(t)
	registerPayment(t, svc, testPayment())

	bad := []Reversal{
		{ReversalID: " ", TxnID: "TXN-1", Amount: 1, Currency: "CNY", Period: "p"},
		{ReversalID: "R", TxnID: "TXN-1", Amount: 0, Currency: "CNY", Period: "p"},
		{ReversalID: "R", TxnID: "TXN-1", Amount: 1, Currency: " ", Period: "p"},
		{ReversalID: "R", TxnID: "TXN-1", Amount: 1, Currency: "CNY", Period: " "},
	}
	for i, reversal := range bad {
		if _, err := svc.Reverse(context.Background(), reversal); !errors.Is(err, ErrInvalidReversal) {
			t.Fatalf("bad reversal #%d error = %v, want ErrInvalidReversal", i, err)
		}
	}

	_, err := svc.Reverse(context.Background(), Reversal{
		ReversalID: "R", TxnID: "UNKNOWN", Amount: 1, Currency: "CNY", Period: "2026-11",
	})
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("unknown reversal txn error = %v, want ErrPaymentNotFound", err)
	}
}

func TestCancelledContextRejected(t *testing.T) {
	svc, _ := newTestService(t)
	registerPayment(t, svc, testPayment())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.Register(ctx, Payment{TxnID: "X", Amount: 1, Currency: "CNY", Period: "p"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Register with cancelled ctx error = %v", err)
	}
	if _, err := svc.Confirm(ctx, matchingConfirmation("C-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Confirm with cancelled ctx error = %v", err)
	}
	if _, err := svc.Reverse(ctx, matchingReversal("R-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reverse with cancelled ctx error = %v", err)
	}
	if _, err := svc.Get(ctx, "TXN-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get with cancelled ctx error = %v", err)
	}
}

func TestMemoryStoreMutateRejectsNilCallback(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Mutate(context.Background(), "TXN-1", nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("nil mutate error = %v, want ErrInvalidState", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
