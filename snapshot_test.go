package netsec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestThresholdBoundaryAndBudget(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)

	snap, err := store.Create(ctx, "r1", 5, RecordConfig{Threshold: 5, Budget: 10})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Count equal to threshold is publishable.
	entry, err := snap.Publish(ctx, "v1", 3)
	if err != nil {
		t.Fatalf("publish at threshold: %v", err)
	}
	if entry.Version != 1 || entry.Kind != AuditKindPublish || entry.Value != "v1" ||
		entry.Cost != 3 || entry.BudgetRemaining != 7 || entry.Count != 5 {
		t.Fatalf("unexpected audit entry: %+v", entry)
	}

	view, err := snap.View(ctx)
	if err != nil || view.Count != 5 || view.Threshold != 5 ||
		view.BudgetRemaining != 7 || view.Withdrawn {
		t.Fatalf("unexpected view: %+v err=%v", view, err)
	}

	// Cost above the remaining budget is rejected; the budget is untouched.
	if _, err := snap.Publish(ctx, "x", 8); !errors.Is(err, ErrInsufficientBudget) {
		t.Fatalf("want ErrInsufficientBudget, got %v", err)
	}

	// Spending the exact remaining budget succeeds.
	if _, err := snap.Publish(ctx, "v2", 7); err != nil {
		t.Fatalf("publish exact budget: %v", err)
	}

	// Zero-cost publishes still pass the threshold after the budget is spent.
	if _, err := snap.Publish(ctx, "v3", 0); err != nil {
		t.Fatalf("zero-cost publish: %v", err)
	}
	if _, err := snap.Publish(ctx, "x", 1); !errors.Is(err, ErrInsufficientBudget) {
		t.Fatalf("want ErrInsufficientBudget after exhaustion, got %v", err)
	}

	// Withdrawal below the threshold: the old snapshot is withdrawn and the
	// new current version refuses to publish even at zero cost.
	if _, err := store.Withdraw(ctx, "r1", 4, 0); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if _, err := snap.Publish(ctx, "stale", 0); !errors.Is(err, ErrVersionWithdrawn) {
		t.Fatalf("old snapshot publish: want ErrVersionWithdrawn, got %v", err)
	}
	cur, err := store.CurrentSnapshot(ctx, "r1")
	if err != nil {
		t.Fatalf("current snapshot: %v", err)
	}
	if _, err := cur.Publish(ctx, "v4", 0); !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("below-threshold publish: want ErrBelowThreshold, got %v", err)
	}

	// Argument and existence errors.
	if _, err := snap.Publish(ctx, "x", -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative cost: want ErrInvalidArgument, got %v", err)
	}
	if _, err := store.Create(ctx, "r1", 1, RecordConfig{Threshold: 1, Budget: 1}); !errors.Is(err, ErrRecordExists) {
		t.Fatalf("duplicate create: want ErrRecordExists, got %v", err)
	}
	if _, err := store.CurrentSnapshot(ctx, "missing"); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing record: want ErrRecordNotFound, got %v", err)
	}
}

func TestWithdrawBlocksOldSnapshotAndKeepsAudit(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)

	snap, err := store.Create(ctx, "u", 10, RecordConfig{Threshold: 10, Budget: 100})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	published, err := snap.Publish(ctx, "kept", 10)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	next, err := store.Withdraw(ctx, "u", 2, 90)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	// The old version remains readable for inspection but is flagged withdrawn.
	oldView, err := snap.View(ctx)
	if err != nil {
		t.Fatalf("old view: %v", err)
	}
	if oldView.Version != 1 || !oldView.Withdrawn || oldView.Count != 10 ||
		oldView.BudgetRemaining != 90 {
		t.Fatalf("unexpected old view: %+v", oldView)
	}

	// No new result may ever be released from the withdrawn snapshot.
	if _, err := snap.Publish(ctx, "leak", 0); !errors.Is(err, ErrVersionWithdrawn) {
		t.Fatalf("stale publish: want ErrVersionWithdrawn, got %v", err)
	}

	// The new version is a fresh fixed snapshot with the shrunken population.
	if next.Version() != 2 {
		t.Fatalf("new version = %d, want 2", next.Version())
	}
	newView, err := next.View(ctx)
	if err != nil {
		t.Fatalf("new view: %v", err)
	}
	if newView.Count != 2 || newView.Threshold != 10 || newView.BudgetRemaining != 90 || newView.Withdrawn {
		t.Fatalf("unexpected new view: %+v", newView)
	}
	if _, err := next.Publish(ctx, "x", 0); !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("sub-threshold new version: want ErrBelowThreshold, got %v", err)
	}

	// The already published result survives in the append-only audit trail.
	trail, err := store.Audit(ctx, "u")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(trail) != 3 {
		t.Fatalf("audit len = %d, want 3 (create, publish, withdraw)", len(trail))
	}
	if trail[0].Kind != AuditKindCreate || trail[1].Kind != AuditKindPublish ||
		trail[2].Kind != AuditKindWithdraw {
		t.Fatalf("unexpected audit kinds: %+v", trail)
	}
	kept := trail[1]
	if kept.Seq != published.Seq || kept.Value != "kept" || kept.Cost != 10 ||
		kept.BudgetRemaining != 90 || kept.Version != 1 || kept.Count != 10 {
		t.Fatalf("published audit entry altered: %+v", kept)
	}

	// Audit returns a defensive copy; mutating it cannot change the trail.
	trail[1].Value = "tampered"
	trail2, _ := store.Audit(ctx, "u")
	if trail2[1].Value != "kept" {
		t.Fatalf("audit trail is mutable through returned slice: %+v", trail2[1])
	}
}

// barrier is a two-phase synchronization point used from inside a storage
// transaction hook. It lets a test park a transaction at its commit boundary
// while another goroutine attempts a conflicting transaction.
type barrier struct {
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBarrier() *barrier {
	return &barrier{reached: make(chan struct{}), release: make(chan struct{})}
}

// signalReached closes reached exactly once, so a barrier is safe if the same
// hook function is ever invoked more than once during a test.
func (b *barrier) signalReached() {
	b.once.Do(func() { close(b.reached) })
}

func TestBarrierPublishThenWithdraw(t *testing.T) {
	ctx := context.Background()
	pb := newBarrier()
	store := NewStore(&Hooks{
		BeforePublishCommit: func(ctx context.Context, _ string, _ VersionView) error {
			close(pb.reached)
			select {
			case <-pb.release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	snap, err := store.Create(ctx, "w", 10, RecordConfig{Threshold: 10, Budget: 100})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Phase 1: a publish transaction parks inside its commit barrier, holding
	// the record lock after all threshold and budget checks have passed.
	pubErr := make(chan error, 1)
	go func() {
		_, err := snap.Publish(ctx, "a", 5)
		pubErr <- err
	}()
	<-pb.reached

	// Phase 2: withdrawal must block behind the in-flight publish rather than
	// withdraw a population whose result is about to be released.
	wdErr := make(chan error, 1)
	go func() {
		_, err := store.Withdraw(ctx, "w", 1, 95)
		wdErr <- err
	}()
	select {
	case err := <-wdErr:
		t.Fatalf("withdraw committed while publish transaction was open: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// Releasing the publish commits it first; the queued withdrawal then wins
	// against every subsequent publish from the old snapshot.
	close(pb.release)
	if err := <-pubErr; err != nil {
		t.Fatalf("parked publish failed: %v", err)
	}
	if err := <-wdErr; err != nil {
		t.Fatalf("queued withdraw failed: %v", err)
	}

	if _, err := snap.Publish(ctx, "b", 0); !errors.Is(err, ErrVersionWithdrawn) {
		t.Fatalf("post-withdraw old publish: want ErrVersionWithdrawn, got %v", err)
	}
	oldView, _ := snap.View(ctx)
	if !oldView.Withdrawn || oldView.BudgetRemaining != 95 {
		t.Fatalf("old version state wrong: %+v", oldView)
	}
	cur, _ := store.CurrentSnapshot(ctx, "w")
	curView, _ := cur.View(ctx)
	if curView.Version != 2 || curView.Count != 1 || curView.BudgetRemaining != 95 {
		t.Fatalf("new version state wrong: %+v", curView)
	}

	trail, _ := store.Audit(ctx, "w")
	if len(trail) != 3 || trail[1].Kind != AuditKindPublish || trail[1].Value != "a" ||
		trail[2].Kind != AuditKindWithdraw {
		t.Fatalf("unexpected audit trail: %+v", trail)
	}
}

func TestBarrierWithdrawThenPublish(t *testing.T) {
	ctx := context.Background()
	wb := newBarrier()
	store := NewStore(&Hooks{
		BeforeWithdrawCommit: func(context.Context, string, VersionView) error {
			close(wb.reached)
			<-wb.release
			return nil
		},
	})
	snap, err := store.Create(ctx, "w", 10, RecordConfig{Threshold: 10, Budget: 100})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Phase 1: the withdrawal parks at its commit barrier holding the record
	// lock, so the racing publish cannot observe the old current version.
	wdErr := make(chan error, 1)
	go func() {
		_, err := store.Withdraw(ctx, "w", 1, 100)
		wdErr <- err
	}()
	<-wb.reached

	pubErr := make(chan error, 1)
	go func() {
		_, err := snap.Publish(ctx, "racer", 1)
		pubErr <- err
	}()
	select {
	case err := <-pubErr:
		t.Fatalf("publish committed while withdrawal transaction was open: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// Once the withdrawal commits, the parked publish wakes up and must refuse
	// to release a result from the now-withdrawn snapshot.
	close(wb.release)
	if err := <-wdErr; err != nil {
		t.Fatalf("parked withdraw failed: %v", err)
	}
	if err := <-pubErr; !errors.Is(err, ErrVersionWithdrawn) {
		t.Fatalf("racing publish: want ErrVersionWithdrawn, got %v", err)
	}

	// No publish audit entry exists, and no budget was spent on the old version.
	trail, _ := store.Audit(ctx, "w")
	if len(trail) != 2 || trail[0].Kind != AuditKindCreate || trail[1].Kind != AuditKindWithdraw {
		t.Fatalf("unexpected audit trail: %+v", trail)
	}
	oldView, _ := snap.View(ctx)
	if !oldView.Withdrawn || oldView.BudgetRemaining != 100 {
		t.Fatalf("old version should keep full budget: %+v", oldView)
	}
}

func TestConcurrentPublishesRespectBudget(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	snap, err := store.Create(ctx, "b", 10, RecordConfig{Threshold: 10, Budget: 100})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	const workers = 200
	var ok, rejected int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := snap.Publish(ctx, i, 1)
			switch {
			case err == nil:
				atomic.AddInt64(&ok, 1)
			case errors.Is(err, ErrInsufficientBudget):
				atomic.AddInt64(&rejected, 1)
			default:
				t.Errorf("unexpected publish error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if ok != 100 {
		t.Fatalf("successful publishes = %d, want exactly 100 (budget)", ok)
	}
	if rejected != workers-100 {
		t.Fatalf("rejected = %d, want %d", rejected, workers-100)
	}
	view, _ := snap.View(ctx)
	if view.BudgetRemaining != 0 {
		t.Fatalf("remaining budget = %d, want 0", view.BudgetRemaining)
	}

	// Audit entries carry gap-free per-record sequence numbers and one entry
	// per successful publish, all bound to the pinned version.
	trail, _ := store.Audit(ctx, "b")
	if len(trail) != 101 {
		t.Fatalf("audit len = %d, want 101", len(trail))
	}
	for i, entry := range trail {
		if entry.Seq != uint64(i+1) {
			t.Fatalf("audit seq gap at %d: %d", i, entry.Seq)
		}
		if i > 0 && (entry.Kind != AuditKindPublish || entry.Version != 1) {
			t.Fatalf("unexpected audit entry: %+v", entry)
		}
	}
}

func TestHookAbortRollsBackPublish(t *testing.T) {
	ctx := context.Background()
	abortErr := errors.New("barrier abort")
	store := NewStore(&Hooks{
		BeforePublishCommit: func(context.Context, string, VersionView) error {
			return abortErr
		},
	})
	snap, err := store.Create(ctx, "a", 5, RecordConfig{Threshold: 5, Budget: 10})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := snap.Publish(ctx, "x", 4); !errors.Is(err, abortErr) {
		t.Fatalf("want hook error, got %v", err)
	}

	// The aborted transaction left no trace: budget intact, no publish audit.
	view, _ := snap.View(ctx)
	if view.BudgetRemaining != 10 {
		t.Fatalf("budget changed by aborted txn: %d", view.BudgetRemaining)
	}
	trail, _ := store.Audit(ctx, "a")
	if len(trail) != 1 || trail[0].Kind != AuditKindCreate {
		t.Fatalf("aborted publish wrote audit entries: %+v", trail)
	}

	// After rollback the record is usable again; a later commit still works.
	plain := NewStore(nil)
	s2, _ := plain.Create(ctx, "a", 5, RecordConfig{Threshold: 5, Budget: 10})
	if _, err := s2.Publish(ctx, "ok", 4); err != nil {
		t.Fatalf("publish after rollback: %v", err)
	}
}

func TestContextCanceledAtPublishBarrier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pb := newBarrier()
	store := NewStore(&Hooks{
		BeforePublishCommit: func(ctx context.Context, _ string, _ VersionView) error {
			pb.signalReached()
			select {
			case <-pb.release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	snap, err := store.Create(ctx, "c", 5, RecordConfig{Threshold: 5, Budget: 10})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pubErr := make(chan error, 1)
	go func() {
		_, err := snap.Publish(ctx, "x", 1)
		pubErr <- err
	}()
	<-pb.reached
	cancel()

	if err := <-pubErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	view, _ := snap.View(context.Background())
	if view.BudgetRemaining != 10 {
		t.Fatalf("canceled txn changed budget: %d", view.BudgetRemaining)
	}
	trail, _ := store.Audit(context.Background(), "c")
	if len(trail) != 1 {
		t.Fatalf("canceled txn wrote audit: %+v", trail)
	}
}

func TestChainedWithdrawalsPinEachVersion(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	v1, err := store.Create(ctx, "k", 10, RecordConfig{Threshold: 5, Budget: 50})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	v2, err := store.Withdraw(ctx, "k", 6, 40)
	if err != nil {
		t.Fatalf("withdraw 1: %v", err)
	}
	if _, err := v2.Publish(ctx, "on-v2", 10); err != nil {
		t.Fatalf("publish from v2: %v", err)
	}

	v3, err := store.Withdraw(ctx, "k", 3, 20)
	if err != nil {
		t.Fatalf("withdraw 2: %v", err)
	}

	// Every old snapshot is sealed once a newer version is installed.
	if _, err := v1.Publish(ctx, "x", 0); !errors.Is(err, ErrVersionWithdrawn) {
		t.Fatalf("v1 publish: want ErrVersionWithdrawn, got %v", err)
	}
	if _, err := v2.Publish(ctx, "x", 0); !errors.Is(err, ErrVersionWithdrawn) {
		t.Fatalf("v2 publish: want ErrVersionWithdrawn, got %v", err)
	}
	if _, err := v3.Publish(ctx, "x", 0); !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("v3 publish: want ErrBelowThreshold, got %v", err)
	}

	// Old versions stay readable for audit and investigation.
	old, _ := v1.View(ctx)
	mid, _ := v2.View(ctx)
	cur, _ := v3.View(ctx)
	if old.Version != 1 || !old.Withdrawn || mid.Version != 2 || !mid.Withdrawn ||
		cur.Version != 3 || cur.Withdrawn || cur.Count != 3 || cur.BudgetRemaining != 20 {
		t.Fatalf("version chain wrong: %+v %+v %+v", old, mid, cur)
	}

	trail, _ := store.Audit(ctx, "k")
	if len(trail) != 4 {
		t.Fatalf("audit len = %d, want 4", len(trail))
	}
	publishOnV2 := trail[2]
	if publishOnV2.Value != "on-v2" || publishOnV2.Version != 2 || publishOnV2.BudgetRemaining != 30 {
		t.Fatalf("v2 publish audit wrong: %+v", publishOnV2)
	}
}

func TestWithdrawValidation(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)
	if _, err := store.Create(ctx, "v", 5, RecordConfig{Threshold: 5, Budget: 10}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Withdrawal cannot grow the protected population or budget.
	if _, err := store.Withdraw(ctx, "v", 6, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("growing count: want ErrInvalidArgument, got %v", err)
	}
	if _, err := store.Withdraw(ctx, "v", 5, 11); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("growing budget: want ErrInvalidArgument, got %v", err)
	}
	if _, err := store.Withdraw(ctx, "v", -1, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative count: want ErrInvalidArgument, got %v", err)
	}
	if _, err := store.Withdraw(ctx, "missing", 0, 0); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing record: want ErrRecordNotFound, got %v", err)
	}

	// A rejected withdrawal leaves version 1 current and fully publishable.
	snap, _ := store.CurrentSnapshot(ctx, "v")
	if _, err := snap.Publish(ctx, "still-ok", 10); err != nil {
		t.Fatalf("publish after rejected withdraw: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	store := NewStore(nil)

	cases := []struct {
		name   string
		record string
		count  int
		cfg    RecordConfig
	}{
		{"empty record", "", 1, RecordConfig{Threshold: 1, Budget: 1}},
		{"negative count", "x", -1, RecordConfig{Threshold: 1, Budget: 1}},
		{"zero threshold", "x", 1, RecordConfig{Threshold: 0, Budget: 1}},
		{"negative budget", "x", 1, RecordConfig{Threshold: 1, Budget: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Create(ctx, tc.record, tc.count, tc.cfg); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want ErrInvalidArgument, got %v", err)
			}
		})
	}

	// A record created below threshold reads normally but cannot publish.
	snap, err := store.Create(ctx, "small", 2, RecordConfig{Threshold: 3, Budget: 10})
	if err != nil {
		t.Fatalf("create below threshold: %v", err)
	}
	view, err := snap.View(ctx)
	if err != nil || view.Count != 2 || view.Threshold != 3 {
		t.Fatalf("view: %+v err=%v", view, err)
	}
	if _, err := snap.Publish(ctx, "x", 0); !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("want ErrBelowThreshold, got %v", err)
	}
}

func TestDistinctRecordsDoNotBlockEachOther(t *testing.T) {
	ctx := context.Background()
	pb := newBarrier()
	store := NewStore(&Hooks{
		BeforePublishCommit: func(_ context.Context, record string, _ VersionView) error {
			if record == "held" {
				close(pb.reached)
				<-pb.release
			}
			return nil
		},
	})
	if _, err := store.Create(ctx, "held", 5, RecordConfig{Threshold: 5, Budget: 10}); err != nil {
		t.Fatalf("create held: %v", err)
	}
	other, err := store.Create(ctx, "other", 5, RecordConfig{Threshold: 5, Budget: 10})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	held, _ := store.CurrentSnapshot(ctx, "held")
	blocked := make(chan struct{})
	go func() {
		_, _ = held.Publish(ctx, "parked", 1)
		close(blocked)
	}()
	<-pb.reached

	// The parked transaction holds the "held" record lock only; "other" stays
	// fully available for its own threshold check, budget charge and audit.
	done := make(chan error, 1)
	go func() {
		_, err := other.Publish(ctx, "independent", 2)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("independent record publish failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("independent record was blocked by another record's transaction")
	}

	close(pb.release)
	<-blocked
}
