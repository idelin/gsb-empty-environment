package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	svc  *Attributor
	now  *time.Time
	acq1 Acquirer
	acq2 Acquirer
	m1   Merchant
	m2   Merchant
	a1   SettlementAccount
	a2   SettlementAccount
	t1   TerminalBinding
	t2   TerminalBinding
}

func newFixture(t *testing.T, options ...Option) fixture {
	t.Helper()

	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	base := []Option{WithClock(clock), WithRebindDelay(2 * time.Hour)}
	options = append(base, options...)

	svc := NewAttributor(options...)
	f := fixture{
		svc:  svc,
		now:  &now,
		acq1: Acquirer{ID: "acq-1", Name: "Acquirer One"},
		acq2: Acquirer{ID: "acq-2", Name: "Acquirer Two"},
		m1:   Merchant{ID: "merchant-1", Name: "Merchant One"},
		m2:   Merchant{ID: "merchant-2", Name: "Merchant Two"},
		a1: SettlementAccount{
			ID: "account-1", MerchantID: "merchant-1", AcquirerID: "acq-1", Currency: "CNY",
		},
		a2: SettlementAccount{
			ID: "account-2", MerchantID: "merchant-2", AcquirerID: "acq-2", Currency: "CNY",
		},
		t1: TerminalBinding{
			TerminalID: "terminal-1", MerchantID: "merchant-1",
			AcquirerID: "acq-1", AccountID: "account-1", Currency: "CNY",
		},
		t2: TerminalBinding{
			TerminalID: "terminal-2", MerchantID: "merchant-2",
			AcquirerID: "acq-2", AccountID: "account-2", Currency: "CNY",
		},
	}

	for _, step := range []func() error{
		func() error { return svc.RegisterAcquirer(f.acq1) },
		func() error { return svc.RegisterAcquirer(f.acq2) },
		func() error { return svc.RegisterMerchant(f.m1) },
		func() error { return svc.RegisterMerchant(f.m2) },
		func() error { return svc.RegisterSettlementAccount(f.a1) },
		func() error { return svc.RegisterSettlementAccount(f.a2) },
		func() error { return svc.SignTerminal(f.t1) },
		func() error { return svc.SignTerminal(f.t2) },
	} {
		if err := step(); err != nil {
			t.Fatalf("fixture setup: %v", err)
		}
	}
	return f
}

func (f fixture) request(amount int64) SettlementRequest {
	return SettlementRequest{
		Reference:   "settlement-1",
		TerminalID:  f.t1.TerminalID,
		MerchantID:  f.t1.MerchantID,
		AcquirerID:  f.t1.AcquirerID,
		AccountID:   f.t1.AccountID,
		Currency:    "CNY",
		AmountCents: amount,
	}
}

func TestLegalSettlementUsesOnlySignedAccount(t *testing.T) {
	f := newFixture(t)

	request := f.request(1_250)
	settlement, err := f.svc.Attribute(request)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if settlement.AccountID != f.a1.ID {
		t.Fatalf("destination = %q, want %q", settlement.AccountID, f.a1.ID)
	}
	if settlement.DestinationAccountID() != f.a1.ID {
		t.Fatalf("DestinationAccountID = %q, want %q", settlement.DestinationAccountID(), f.a1.ID)
	}
	if settlement.MerchantID != f.m1.ID || settlement.AcquirerID != f.acq1.ID {
		t.Fatalf("unexpected attribution: %+v", settlement)
	}
	if settlement.Currency != "CNY" || settlement.AmountCents != request.AmountCents {
		t.Fatalf("unexpected amount or currency: %+v", settlement)
	}
	audited, ok := f.svc.Settlement(request.Reference)
	if !ok || audited.AccountID != f.a1.ID {
		t.Fatalf("settlement audit = %+v, %v", audited, ok)
	}

	// Omitting account and currency still resolves them from the signed binding.
	request.Reference = "settlement-2"
	request.AccountID = ""
	request.Currency = ""
	settlement, err = f.svc.Attribute(request)
	if err != nil {
		t.Fatalf("Attribute without account/currency: %v", err)
	}
	if settlement.AccountID != f.a1.ID || settlement.Currency != "CNY" {
		t.Fatalf("derived destination = %+v", settlement)
	}
}

func TestPayoutGatewayReceivesOnlySignedAccount(t *testing.T) {
	f := newFixture(t)
	var paid []Settlement
	gateway := payoutFunc(func(_ context.Context, settlement Settlement) error {
		paid = append(paid, settlement)
		return nil
	})

	settlement, err := f.svc.Settle(context.Background(), f.request(2_000), gateway)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if settlement.AccountID != f.a1.ID {
		t.Fatalf("settlement account = %q", settlement.AccountID)
	}
	if len(paid) != 1 || paid[0].AccountID != f.a1.ID {
		t.Fatalf("payout destinations = %+v", paid)
	}

	bad := f.request(2_000)
	bad.Reference = "bad-payout"
	bad.AccountID = f.a2.ID
	if _, err := f.svc.Settle(context.Background(), bad, gateway); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("Settle with substituted account error = %v, want account mismatch", err)
	}
	if len(paid) != 1 {
		t.Fatalf("gateway was called for a rejected settlement: %+v", paid)
	}
}

type payoutFunc func(context.Context, Settlement) error

func (f payoutFunc) Pay(ctx context.Context, settlement Settlement) error {
	return f(ctx, settlement)
}

func TestRejectsTerminalSubstitution(t *testing.T) {
	f := newFixture(t)

	request := f.request(1_000)
	request.TerminalID = "attacker-terminal"
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrUnsignedTerminal) {
		t.Fatalf("unknown terminal error = %v, want unsigned terminal", err)
	}

	// A valid terminal belonging to a different merchant cannot be inserted
	// into this merchant's settlement.
	request = f.request(1_000)
	request.TerminalID = f.t2.TerminalID
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrCrossMerchant) {
		t.Fatalf("foreign terminal error = %v, want cross merchant", err)
	}

	if err := f.svc.SignTerminal(f.t1); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("re-signing existing terminal error = %v, want already exists", err)
	}
}

func TestRejectsMerchantAcquirerAndAccountSwaps(t *testing.T) {
	f := newFixture(t)

	request := f.request(1_000)
	request.MerchantID = f.m2.ID
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrCrossMerchant) {
		t.Fatalf("merchant swap error = %v, want cross merchant", err)
	}

	request = f.request(1_000)
	request.AcquirerID = f.acq2.ID
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrCrossAcquirer) {
		t.Fatalf("acquirer swap error = %v, want cross acquirer", err)
	}

	request = f.request(1_000)
	request.AccountID = f.a2.ID
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("account swap error = %v, want account mismatch", err)
	}

	request = f.request(1_000)
	request.Currency = "USD"
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("currency swap error = %v, want currency mismatch", err)
	}
}

func TestContractRegistrationRejectsCrossBoundaries(t *testing.T) {
	f := newFixture(t)

	crossAccount := SettlementAccount{
		ID: "cross-account", MerchantID: f.m1.ID, AcquirerID: f.acq2.ID,
	}
	if err := f.svc.RegisterSettlementAccount(crossAccount); err != nil {
		t.Fatalf("register cross-boundary account: %v", err)
	}

	badBinding := f.t1
	badBinding.TerminalID = "terminal-cross"
	badBinding.AccountID = crossAccount.ID
	if err := f.svc.SignTerminal(badBinding); !errors.Is(err, ErrCrossAcquirer) {
		t.Fatalf("signing cross-acquirer account error = %v, want cross acquirer", err)
	}

	badBinding.AccountID = f.a2.ID
	if err := f.svc.SignTerminal(badBinding); !errors.Is(err, ErrCrossMerchant) {
		t.Fatalf("signing foreign merchant account error = %v, want cross merchant", err)
	}
}

func TestAmountValidationAndIdempotency(t *testing.T) {
	f := newFixture(t, WithMaxAmount(10_000))

	for _, amount := range []int64{0, -1} {
		request := f.request(amount)
		if _, err := f.svc.Attribute(request); !errors.Is(err, ErrAmountTooSmall) || !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("amount %d error = %v, want amount too small", amount, err)
		}
	}
	request := f.request(10_001)
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrAmountTooLarge) || !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("large amount error = %v, want amount too large", err)
	}

	legal := f.request(3_000)
	first, err := f.svc.Attribute(legal)
	if err != nil {
		t.Fatalf("first Attribute: %v", err)
	}
	second, err := f.svc.Attribute(legal)
	if err != nil {
		t.Fatalf("replayed Attribute: %v", err)
	}
	if second.AccountID != f.a1.ID || !second.SettledAt.Equal(first.SettledAt) {
		t.Fatalf("idempotent replay changed result: first=%+v second=%+v", first, second)
	}

	changed := legal
	changed.AmountCents = 3_001
	if _, err := f.svc.Attribute(changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed replay error = %v, want idempotency conflict", err)
	}

	changed = legal
	changed.AccountID = f.a2.ID
	if _, err := f.svc.Attribute(changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("destination replay error = %v, want idempotency conflict", err)
	}
}

func TestMerchantAndAccountAmountLimits(t *testing.T) {
	f := newFixture(t, WithMaxAmount(100_000))
	f.m1.MaxSettlementCents = 4_000
	f.a1.MaxSettlementCents = 3_000

	// Limits are copied at registration, so use a fresh registry with the
	// configured limits.
	svc := NewAttributor(
		WithClock(func() time.Time { return *f.now }),
		WithRebindDelay(time.Hour),
		WithMaxAmount(100_000),
	)
	for _, register := range []func() error{
		func() error { return svc.RegisterAcquirer(f.acq1) },
		func() error { return svc.RegisterMerchant(f.m1) },
		func() error { return svc.RegisterAccount(f.a1) },
		func() error { return svc.SignTerminal(f.t1) },
	} {
		if err := register(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	request := f.request(3_001)
	if _, err := svc.Attribute(request); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("account-limit error = %v, want amount too large", err)
	}
	request = f.request(3_000)
	if _, err := svc.Attribute(request); err != nil {
		t.Fatalf("amount at account limit: %v", err)
	}

	f.a1.MaxSettlementCents = 0
	svc2 := NewAttributor(WithMaxAmount(100_000))
	for _, register := range []func() error{
		func() error { return svc2.RegisterAcquirer(f.acq1) },
		func() error { return svc2.RegisterMerchant(f.m1) },
		func() error { return svc2.RegisterAccount(f.a1) },
		func() error { return svc2.SignTerminal(f.t1) },
	} {
		if err := register(); err != nil {
			t.Fatalf("setup svc2: %v", err)
		}
	}
	request.Reference = "merchant-limit"
	request.AmountCents = 4_001
	if _, err := svc2.Attribute(request); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("merchant-limit error = %v, want amount too large", err)
	}
}

func TestRebindReviewAndActivationDelay(t *testing.T) {
	f := newFixture(t)

	proposal := TerminalBinding{
		TerminalID: f.t1.TerminalID,
		MerchantID: f.m2.ID,
		AcquirerID: f.acq2.ID,
		AccountID:  f.a2.ID,
		Currency:   "CNY",
	}
	rebind, err := f.svc.RequestRebind(f.t1.TerminalID, proposal, "operations")
	if err != nil {
		t.Fatalf("RequestRebind: %v", err)
	}
	if rebind.Status != RebindPendingReview || !rebind.EffectiveAt.IsZero() {
		t.Fatalf("initial rebind state = %+v", rebind)
	}

	// Before approval, the old signed binding remains authoritative.
	newRequest := f.request(1_500)
	newRequest.Reference = "before-approval-new"
	newRequest.MerchantID = f.m2.ID
	newRequest.AcquirerID = f.acq2.ID
	newRequest.AccountID = f.a2.ID
	if _, err := f.svc.Attribute(newRequest); !errors.Is(err, ErrCrossMerchant) || !errors.Is(err, ErrRebindNotApproved) {
		t.Fatalf("pre-approval new binding error = %v, want cross merchant/not approved", err)
	}

	oldRequest := f.request(1_500)
	oldRequest.Reference = "before-approval-old"
	old, err := f.svc.Attribute(oldRequest)
	if err != nil {
		t.Fatalf("old binding before approval: %v", err)
	}
	if old.AccountID != f.a1.ID {
		t.Fatalf("old settlement destination = %q, want %q", old.AccountID, f.a1.ID)
	}

	if _, err := f.svc.RequestRebind(f.t1.TerminalID, proposal, "operations"); !errors.Is(err, ErrRebindPending) {
		t.Fatalf("second pending rebind error = %v, want rebind pending", err)
	}
	if _, err := f.svc.ApproveRebind(rebind.ID, ""); !errors.Is(err, ErrReviewerRequired) {
		t.Fatalf("blank reviewer error = %v, want reviewer required", err)
	}

	approved, err := f.svc.ApproveRebind(rebind.ID, "reviewer-42")
	if err != nil {
		t.Fatalf("ApproveRebind: %v", err)
	}
	if approved.Status != RebindApproved {
		t.Fatalf("approved status = %s", approved.Status)
	}
	wantEffective := f.now.Add(2 * time.Hour)
	if !approved.EffectiveAt.Equal(wantEffective) {
		t.Fatalf("effective at = %s, want %s", approved.EffectiveAt, wantEffective)
	}

	// A rebind cannot be activated immediately after approval.
	*f.now = wantEffective.Add(-time.Nanosecond)
	oldRequest.Reference = "within-delay-old"
	newRequest.Reference = "within-delay-new"
	if _, err := f.svc.Attribute(newRequest); !errors.Is(err, ErrCrossMerchant) || !errors.Is(err, ErrRebindNotEffective) {
		t.Fatalf("new binding within delay error = %v, want cross merchant/not effective", err)
	}
	old, err = f.svc.Attribute(oldRequest)
	if err != nil || old.AccountID != f.a1.ID {
		t.Fatalf("old binding within delay = %+v, %v", old, err)
	}

	// At the deadline the complete tuple switches atomically.
	*f.now = wantEffective
	newRequest.Reference = "effective-new"
	oldRequest.Reference = "effective-old"
	payout, err := f.svc.Attribute(newRequest)
	if err != nil {
		t.Fatalf("new binding at effective time: %v", err)
	}
	if payout.AccountID != f.a2.ID || payout.MerchantID != f.m2.ID || payout.AcquirerID != f.acq2.ID {
		t.Fatalf("new binding destination = %+v", payout)
	}
	if _, err := f.svc.Attribute(oldRequest); !errors.Is(err, ErrCrossMerchant) {
		t.Fatalf("old binding after effective time error = %v, want cross merchant", err)
	}

	state, err := f.svc.Rebind(rebind.ID)
	if err != nil {
		t.Fatalf("Rebind audit: %v", err)
	}
	if state.Status != RebindActive || state.ReviewedBy != "reviewer-42" || !state.ActivatedAt.Equal(wantEffective) {
		t.Fatalf("final rebind audit = %+v", state)
	}
	if _, pending := f.svc.PendingRebind(f.t1.TerminalID); pending {
		t.Fatal("rebind still marked pending after activation")
	}
}

func TestRebindRejectsUncontractedDestination(t *testing.T) {
	f := newFixture(t)

	bad := TerminalBinding{
		TerminalID: f.t1.TerminalID,
		MerchantID: f.m2.ID,
		AcquirerID: f.acq2.ID,
		AccountID:  f.a1.ID,
	}
	_, err := f.svc.RequestRebind(f.t1.TerminalID, bad, "operations")
	if !errors.Is(err, ErrRebindAccountInvalid) || !errors.Is(err, ErrCrossMerchant) {
		t.Fatalf("uncontracted rebind error = %v, want rebind account invalid and cross merchant", err)
	}

	bad.MerchantID = f.m1.ID
	bad.AcquirerID = f.acq2.ID
	bad.AccountID = f.a1.ID
	_, err = f.svc.RequestRebind(f.t1.TerminalID, bad, "operations")
	if !errors.Is(err, ErrRebindAccountInvalid) || !errors.Is(err, ErrCrossAcquirer) {
		t.Fatalf("cross-acquirer rebind error = %v, want rebind account invalid and cross acquirer", err)
	}
}

func TestRejectedAndCancelledRebindsKeepOldBinding(t *testing.T) {
	f := newFixture(t)
	proposal := TerminalBinding{
		TerminalID: f.t1.TerminalID,
		MerchantID: f.m2.ID,
		AcquirerID: f.acq2.ID,
		AccountID:  f.a2.ID,
	}

	first, err := f.svc.RequestRebind(f.t1.TerminalID, proposal, "operations")
	if err != nil {
		t.Fatalf("RequestRebind: %v", err)
	}
	if _, err := f.svc.RejectRebind(first.ID, "reviewer", "invalid contract"); err != nil {
		t.Fatalf("RejectRebind: %v", err)
	}
	binding, err := f.svc.CurrentBinding(f.t1.TerminalID)
	if err != nil || binding.AccountID != f.a1.ID || binding.MerchantID != f.m1.ID {
		t.Fatalf("binding after rejection = %+v, %v", binding, err)
	}
	if _, err := f.svc.ApproveRebind(first.ID, "reviewer"); !errors.Is(err, ErrRebindNotPending) {
		t.Fatalf("approve rejected error = %v, want not pending", err)
	}

	second, err := f.svc.RequestRebind(f.t1.TerminalID, proposal, "operations")
	if err != nil {
		t.Fatalf("second RequestRebind: %v", err)
	}
	if _, err := f.svc.ApproveRebind(second.ID, "reviewer"); err != nil {
		t.Fatalf("approve second: %v", err)
	}
	if _, err := f.svc.CancelRebind(second.ID, "operations"); err != nil {
		t.Fatalf("cancel approved rebind: %v", err)
	}
	*f.now = f.now.Add(3 * time.Hour)
	binding, err = f.svc.CurrentBinding(f.t1.TerminalID)
	if err != nil || binding.AccountID != f.a1.ID {
		t.Fatalf("binding after cancellation = %+v, %v", binding, err)
	}
}

func TestInactiveContractsCannotSettle(t *testing.T) {
	f := newFixture(t)

	request := f.request(500)
	if err := f.svc.DeactivateAccount(f.a1.ID); err != nil {
		t.Fatalf("DeactivateAccount: %v", err)
	}
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("inactive account error = %v", err)
	}
	if err := f.svc.ActivateAccount(f.a1.ID); err != nil {
		t.Fatalf("ActivateAccount: %v", err)
	}

	if err := f.svc.DeactivateMerchant(f.m1.ID); err != nil {
		t.Fatalf("DeactivateMerchant: %v", err)
	}
	request.Reference = "inactive-merchant"
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrMerchantInactive) {
		t.Fatalf("inactive merchant error = %v", err)
	}
	if err := f.svc.ActivateMerchant(f.m1.ID); err != nil {
		t.Fatalf("ActivateMerchant: %v", err)
	}

	if err := f.svc.DeactivateTerminal(f.t1.TerminalID); err != nil {
		t.Fatalf("DeactivateTerminal: %v", err)
	}
	request.Reference = "inactive-terminal"
	if _, err := f.svc.Attribute(request); !errors.Is(err, ErrInactiveTerminal) {
		t.Fatalf("inactive terminal error = %v", err)
	}
}

func TestConcurrentRebindApprovalIsSingleWinner(t *testing.T) {
	f := newFixture(t)
	proposal := TerminalBinding{
		TerminalID: f.t1.TerminalID,
		MerchantID: f.m2.ID,
		AcquirerID: f.acq2.ID,
		AccountID:  f.a2.ID,
	}
	rebind, err := f.svc.RequestRebind(f.t1.TerminalID, proposal, "operations")
	if err != nil {
		t.Fatalf("RequestRebind: %v", err)
	}

	const reviewers = 16
	var wg sync.WaitGroup
	errs := make(chan error, reviewers)
	for i := 0; i < reviewers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.ApproveRebind(rebind.ID, "reviewer")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrRebindNotPending) {
			t.Fatalf("unexpected approval error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful approvals = %d, want 1", successes)
	}
}
