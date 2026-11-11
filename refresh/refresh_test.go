package refresh

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// deterministicIDs replaces crypto/rand so failures are readable and every
// identifier is unique within a test.
func deterministicIDs() IDGenerator {
	var n int64
	return func() (string, error) {
		v := atomic.AddInt64(&n, 1)
		return fmt.Sprintf("id-%03d", v), nil
	}
}

func newTestService(store Store) *Service {
	return NewService(store, WithIDGenerator(deterministicIDs()))
}

func mustIssue(t *testing.T, svc *Service) RefreshResult {
	t.Helper()
	res, err := svc.IssueFamily()
	if err != nil {
		t.Fatalf("IssueFamily: %v", err)
	}
	return res
}

func refreshRecover(svc *Service, in RefreshInput) (RefreshResult, error, bool) {
	var (
		res RefreshResult
		err error
	)
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				if e, ok := r.(error); ok {
					err = e
				} else {
					err = fmt.Errorf("%v", r)
				}
			}
		}()
		res, err = svc.Refresh(in)
	}()
	return res, err, panicked
}

func assertTokenStatus(t *testing.T, store *CrashableStore, tokenID string, want TokenStatus) {
	t.Helper()
	data := store.snapshot()
	tok, ok := data.tokens[tokenID]
	if !ok {
		t.Fatalf("token %q missing", tokenID)
	}
	if tok.status != want {
		t.Fatalf("token %q status = %s, want %s", tokenID, tok.status, want)
	}
}

func assertFamilyRevoked(t *testing.T, store *CrashableStore, familyID string) {
	t.Helper()
	data := store.snapshot()
	fam, ok := data.families[familyID]
	if !ok {
		t.Fatalf("family %q missing", familyID)
	}
	if !fam.revoked {
		t.Fatalf("family %q not revoked", familyID)
	}
}

// startBarrier releases all waiting goroutines at once, so concurrent
// refreshes genuinely race inside the service instead of running serially.
type startBarrier struct {
	once  sync.Once
	start chan struct{}
}

func newStartBarrier() *startBarrier {
	return &startBarrier{start: make(chan struct{})}
}

func (b *startBarrier) wait() { <-b.start }
func (b *startBarrier) release() {
	b.once.Do(func() { close(b.start) })
}

// TestIdempotentRetryReturnsSameResult covers the benign case: a mobile
// refresh whose response is lost is retried with the same key and must return
// the exact, already committed result instead of being treated as reuse.
func TestIdempotentRetryReturnsSameResult(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)

	root := mustIssue(t, svc)
	in := RefreshInput{OldTokenID: root.NewTokenID, IdempotencyKey: "key-1"}

	first, err := svc.Refresh(in)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	// Simulate a lost response: the client only retries, state is untouched.
	second, err := svc.Refresh(in)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if first != second {
		t.Fatalf("retry returned %+v, want identical result %+v", second, first)
	}

	data := store.snapshot()
	if len(data.rotations) != 1 {
		t.Fatalf("rotations = %d, want exactly 1", len(data.rotations))
	}
	if _, ok := data.bindings[bindingKey(in.OldTokenID, in.IdempotencyKey)]; !ok {
		t.Fatal("(token, key) binding not persisted")
	}
	assertTokenStatus(t, store, in.OldTokenID, TokenConsumed)
	assertTokenStatus(t, store, first.NewTokenID, TokenUnused)
}

// TestDifferentKeyReuseRevokesFamily is the core security rule: a rotated
// token presented with any other key is reuse and revokes the whole family,
// including tokens issued later in the same family.
func TestDifferentKeyReuseRevokesFamily(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)

	root := mustIssue(t, svc)
	r0 := root.NewTokenID

	r1, err := svc.Refresh(RefreshInput{OldTokenID: r0, IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("rotation r0->r1: %v", err)
	}
	// Grandchild token r2 is issued legitimately from r1.
	r2, err := svc.Refresh(RefreshInput{OldTokenID: r1.NewTokenID, IdempotencyKey: "k2"})
	if err != nil {
		t.Fatalf("rotation r1->r2: %v", err)
	}

	// Stolen/old token r0 is replayed with a different key: reuse detected.
	_, err = svc.Refresh(RefreshInput{OldTokenID: r0, IdempotencyKey: "attacker-key"})
	if !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("reuse refresh err = %v, want ErrFamilyRevoked", err)
	}

	assertFamilyRevoked(t, store, root.FamilyID)
	// Every living token in the family, including the grandchild, is revoked.
	assertTokenStatus(t, store, r0, TokenRevoked)
	assertTokenStatus(t, store, r1.NewTokenID, TokenRevoked)
	assertTokenStatus(t, store, r2.NewTokenID, TokenRevoked)

	// No token from a revoked family can ever be rotated again.
	_, err = svc.Refresh(RefreshInput{OldTokenID: r2.NewTokenID, IdempotencyKey: "k3"})
	if !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("refresh under revoked family err = %v, want ErrFamilyRevoked", err)
	}
}

// TestRetryAfterGrandchildNotTheft proves the ordering between the two rules:
// the same-key retry remains a benign replay even though the old token has
// since been consumed, a grandchild was issued, and the family was later
// revoked through a different-key replay.
func TestRetryAfterGrandchildNotTheft(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)

	root := mustIssue(t, svc)
	r0 := root.NewTokenID

	first, err := svc.Refresh(RefreshInput{OldTokenID: r0, IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("rotation r0->r1: %v", err)
	}
	if _, err := svc.Refresh(RefreshInput{OldTokenID: first.NewTokenID, IdempotencyKey: "k2"}); err != nil {
		t.Fatalf("grandchild rotation: %v", err)
	}
	if _, err := svc.Refresh(RefreshInput{OldTokenID: r0, IdempotencyKey: "evil"}); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("reuse detection err = %v, want ErrFamilyRevoked", err)
	}

	// Original lost response retried with its own key: still the stored result,
	// never a theft verdict, even on an already revoked family.
	retry, err := svc.Refresh(RefreshInput{OldTokenID: r0, IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("same-key retry after revocation: %v", err)
	}
	if retry != first {
		t.Fatalf("retry = %+v, want stored result %+v", retry, first)
	}
}

// TestCrashBeforeCommitLeavesNoState: a crash injected immediately before the
// commit point must not leave any partial rotation behind. After reopening
// the same durable medium, the retry performs a clean rotation.
func TestCrashBeforeCommitLeavesNoState(t *testing.T) {
	disk := NewDisk()
	store := NewCrashableStore(disk)
	svc := newTestService(store)

	root := mustIssue(t, svc)
	in := RefreshInput{OldTokenID: root.NewTokenID, IdempotencyKey: "k1"}
	var armed int32
	store.WithCrashBeforeCommit(func() bool {
		return atomic.CompareAndSwapInt32(&armed, 0, 1)
	})

	_, err, panicked := refreshRecover(svc, in)
	if !panicked || !errors.Is(err, ErrSimulatedCrash) {
		t.Fatalf("expected simulated crash, got panicked=%v err=%v", panicked, err)
	}

	data := store.snapshot()
	if len(data.rotations) != 0 || len(data.bindings) != 0 {
		t.Fatalf("partial write survived pre-commit crash: %+v", data)
	}
	assertTokenStatus(t, store, in.OldTokenID, TokenUnused)

	// Crashed stores refuse further operations until reopened.
	if _, err := svc.Refresh(in); !errors.Is(err, ErrStoreCrashed) {
		t.Fatalf("crashed store err = %v, want ErrStoreCrashed", err)
	}

	reopened := store.Reopen()
	retrySvc := newTestService(reopened)
	res, err := retrySvc.Refresh(in)
	if err != nil {
		t.Fatalf("retry after reopen: %v", err)
	}
	assertTokenStatus(t, reopened, in.OldTokenID, TokenConsumed)
	assertTokenStatus(t, reopened, res.NewTokenID, TokenUnused)
	if len(reopened.snapshot().rotations) != 1 {
		t.Fatal("reopened store did not commit the retry")
	}
}

// TestCrashAfterCommitResponseLost: the commit is durable when the process
// dies, but the response never reaches the client. On reopen, the same-key
// retry must resolve to the already committed result rather than rotate again.
func TestCrashAfterCommitResponseLost(t *testing.T) {
	disk := NewDisk()
	store := NewCrashableStore(disk)
	svc := newTestService(store)

	root := mustIssue(t, svc)
	in := RefreshInput{OldTokenID: root.NewTokenID, IdempotencyKey: "k1"}
	var armed int32
	store.WithCrashAfterCommit(func() bool {
		return atomic.CompareAndSwapInt32(&armed, 0, 1)
	})

	_, err, panicked := refreshRecover(svc, in)
	if !panicked || !errors.Is(err, ErrSimulatedCrash) {
		t.Fatalf("expected post-commit crash, got panicked=%v err=%v", panicked, err)
	}

	// Persist-before-return: despite the lost response, everything is durable.
	data := store.snapshot()
	if len(data.rotations) != 1 {
		t.Fatalf("committed rotation missing after crash, rotations=%d", len(data.rotations))
	}
	assertTokenStatus(t, store, in.OldTokenID, TokenConsumed)

	reopened := store.Reopen()
	retrySvc := newTestService(reopened)
	retry, err := retrySvc.Refresh(in)
	if err != nil {
		t.Fatalf("same-key retry after reopen: %v", err)
	}
	if len(reopened.snapshot().rotations) != 1 {
		t.Fatal("retry created a second rotation instead of replaying the stored one")
	}
	// The grandchild can now be minted normally, proving only one rotation exists.
	grand, err := retrySvc.Refresh(RefreshInput{OldTokenID: retry.NewTokenID, IdempotencyKey: "k2"})
	if err != nil {
		t.Fatalf("grandchild rotation: %v", err)
	}
	assertTokenStatus(t, reopened, grand.NewTokenID, TokenUnused)
}

// TestConcurrentSameKeyBarrier: many simultaneous retries of one lost request
// must collapse into a single rotation and return one identical result.
func TestConcurrentSameKeyBarrier(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)
	root := mustIssue(t, svc)
	in := RefreshInput{OldTokenID: root.NewTokenID, IdempotencyKey: "k1"}

	const n = 16
	barrier := newStartBarrier()
	var wg sync.WaitGroup
	results := make([]RefreshResult, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			barrier.wait()
			results[i], errs[i] = svc.Refresh(in)
		}()
	}
	barrier.release()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got %+v, want %+v", i, results[i], results[0])
		}
	}
	data := store.snapshot()
	if len(data.rotations) != 1 {
		t.Fatalf("rotations = %d, want 1 under concurrent same-key retries", len(data.rotations))
	}
	assertTokenStatus(t, store, results[0].NewTokenID, TokenUnused)
}

// TestConcurrentDifferentKeyBarrier: two racing requests on the same token,
// but with distinct keys. Whichever wins rotates once; the loser is reuse and
// revokes the entire family, including the token the winner just minted.
func TestConcurrentDifferentKeyBarrier(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)
	root := mustIssue(t, svc)

	barrier := newStartBarrier()
	var wg sync.WaitGroup
	type outcome struct {
		res RefreshResult
		err error
	}
	outcomes := make([]outcome, 2)
	inputs := []RefreshInput{
		{OldTokenID: root.NewTokenID, IdempotencyKey: "k1"},
		{OldTokenID: root.NewTokenID, IdempotencyKey: "k2"},
	}
	wg.Add(2)
	for i := range inputs {
		i := i
		go func() {
			defer wg.Done()
			barrier.wait()
			outcomes[i].res, outcomes[i].err = svc.Refresh(inputs[i])
		}()
	}
	barrier.release()
	wg.Wait()

	var winners int
	var minted string
	for _, o := range outcomes {
		switch {
		case o.err == nil:
			winners++
			minted = o.res.NewTokenID
		case errors.Is(o.err, ErrFamilyRevoked):
			// expected for the loser
		default:
			t.Fatalf("unexpected refresh error: %v", o.err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 rotation", winners)
	}
	assertFamilyRevoked(t, store, root.FamilyID)
	assertTokenStatus(t, store, minted, TokenRevoked)
	if len(store.snapshot().rotations) != 1 {
		t.Fatalf("rotations = %d, want 1 before the family revocation", len(store.snapshot().rotations))
	}
}

// TestRefreshAndRevocationSingleOrder verifies the single total order between
// rotation and revocation: the durable family event log is gap-free, every
// family's events are strictly increasing, and a revocation is always the
// last event for that family. The barrier makes the two actions race.
func TestRefreshAndRevocationSingleOrder(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)

	const families = 8
	roots := make([]RefreshResult, families)
	for i := range roots {
		roots[i] = mustIssue(t, svc)
	}

	barrier := newStartBarrier()
	var wg sync.WaitGroup
	for i, root := range roots {
		root := root
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			barrier.wait()
			_, _ = svc.Refresh(RefreshInput{
				OldTokenID:     root.NewTokenID,
				IdempotencyKey: fmt.Sprintf("fam-%d-key", i),
			})
		}()
		go func() {
			defer wg.Done()
			barrier.wait()
			_ = svc.RevokeFamily(root.FamilyID)
		}()
	}
	barrier.release()
	wg.Wait()

	data := store.snapshot()
	if int(data.seq) != len(data.events) {
		t.Fatalf("event log gap: seq=%d events=%d", data.seq, len(data.events))
	}

	byFamily := map[string][]Event{}
	for _, e := range data.events {
		byFamily[e.FamilyID] = append(byFamily[e.FamilyID], e)
	}
	for _, root := range roots {
		events := byFamily[root.FamilyID]
		if len(events) < 2 {
			t.Fatalf("family %s has %d events, want issue plus rotation/revocation", root.FamilyID, len(events))
		}
		for i := 1; i < len(events); i++ {
			if events[i].Seq <= events[i-1].Seq {
				t.Fatalf("family %s events not strictly ordered: %+v", root.FamilyID, events)
			}
			if events[i-1].Kind == EventRevocation {
				t.Fatalf("family %s has events after its revocation: %+v", root.FamilyID, events)
			}
		}
		last := events[len(events)-1]
		if last.Kind != EventRevocation {
			t.Fatalf("family %s last event = %v, want revocation", root.FamilyID, last.Kind)
		}
		assertFamilyRevoked(t, store, root.FamilyID)
	}
}

// TestIdempotencyKeyAcrossTokensRejected guards against splicing two retry
// chains by reusing one key with a different old token.
func TestIdempotencyKeyAcrossTokensRejected(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)

	root := mustIssue(t, svc)
	first, err := svc.Refresh(RefreshInput{OldTokenID: root.NewTokenID, IdempotencyKey: "shared"})
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_, err = svc.Refresh(RefreshInput{OldTokenID: first.NewTokenID, IdempotencyKey: "shared"})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("cross-token key reuse err = %v, want ErrIdempotencyConflict", err)
	}
}

// TestUnknownAndEmptyInputs covers basic input validation paths.
func TestUnknownAndEmptyInputs(t *testing.T) {
	store := NewCrashableStore(NewDisk())
	svc := newTestService(store)

	if _, err := svc.Refresh(RefreshInput{OldTokenID: "ghost", IdempotencyKey: "k"}); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("unknown token err = %v, want ErrTokenNotFound", err)
	}
	root := mustIssue(t, svc)
	if _, err := svc.Refresh(RefreshInput{OldTokenID: root.NewTokenID}); !errors.Is(err, ErrEmptyIdempotencyKey) {
		t.Fatalf("empty key err = %v, want ErrEmptyIdempotencyKey", err)
	}
	if err := svc.RevokeFamily("ghost"); !errors.Is(err, ErrFamilyNotFound) {
		t.Fatalf("unknown family revoke err = %v, want ErrFamilyNotFound", err)
	}
}
