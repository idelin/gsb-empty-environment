package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func testOp(id string) Operation {
	return Operation{OpID: id, Actor: "alice", Action: "grant-role", Detail: "admin"}
}

func noopEffect(context.Context) error { return nil }

func newTestCommitter(t *testing.T, disk *MemoryDisk, maxPending int) *Committer {
	t.Helper()
	sink := NewMemorySink(disk, 0, Faults{})
	c, err := New(context.Background(), sink, Config{MaxPending: maxPending})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestCommitHappyPathRecordsTerminalState(t *testing.T) {
	disk := NewMemoryDisk()
	c := newTestCommitter(t, disk, 4)

	var ran atomic.Int32
	res, err := c.Commit(context.Background(), testOp("op-1"),
		func(context.Context) error { ran.Add(1); return nil })
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Status != StatusSucceeded || ran.Load() != 1 {
		t.Fatalf("unexpected result %+v, effect runs=%d", res, ran.Load())
	}

	rec, ok := c.Status("op-1")
	if !ok || rec.Status != StatusSucceeded || rec.Seq != 2 {
		t.Fatalf("terminal record mismatch: %+v ok=%v", rec, ok)
	}
	if pending := c.Pending(); len(pending) != 0 {
		t.Fatalf("unexpected pending after completion: %v", pending)
	}
}

func TestBackpressureBlocksUntilSlotReleased(t *testing.T) {
	disk := NewMemoryDisk()
	c := newTestCommitter(t, disk, 1)

	inside := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan struct{})

	go func() {
		defer close(holderDone)
		_, _ = c.Commit(context.Background(), testOp("op-hold"),
			func(context.Context) error {
				close(inside)
				<-release
				return nil
			})
	}()
	<-inside

	// The only slot is held: a second commit must queue without running.
	var secondRan atomic.Bool
	done := make(chan Result, 1)
	go func() {
		res, _ := c.Commit(context.Background(), testOp("op-wait"),
			func(context.Context) error { secondRan.Store(true); return nil })
		done <- res
	}()

	select {
	case <-done:
		t.Fatal("second commit ran while all audit slots were held")
	default:
	}
	if secondRan.Load() {
		t.Fatal("side effect ran before a slot was available")
	}

	close(release)
	res := <-done
	if res.Status != StatusSucceeded || !secondRan.Load() {
		t.Fatalf("queued commit did not complete correctly: %+v", res)
	}
	<-holderDone
}

func TestBackpressureRespectsContextCancellation(t *testing.T) {
	disk := NewMemoryDisk()
	c := newTestCommitter(t, disk, 1)

	inside := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		_, _ = c.Commit(context.Background(), testOp("op-hold"),
			func(context.Context) error { close(inside); <-release; return nil })
	}()
	<-inside

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var ran atomic.Bool
	if _, err := c.Commit(ctx, testOp("op-cancel"),
		func(context.Context) error { ran.Store(true); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if ran.Load() {
		t.Fatal("side effect ran for a cancelled, unslotted commit")
	}
	close(release)
	<-holderDone
}

func TestSinkFullIsBackpressureWithoutSideEffect(t *testing.T) {
	disk := NewMemoryDisk()

	// Derive the byte budget from real frame sizes instead of magic numbers.
	// Simulate a host that restarted with a log of historical terminal
	// records: three distinct terminals fill the bounded medium. A new
	// reservation cannot fit even after compaction (distinct OpIDs are not
	// mergeable), so it returns ErrSinkFull and the effect must not run.
	seed := testOp("op-history")
	seedTerminal := Record{Seq: 2, OpID: seed.OpID, Actor: seed.Actor, Action: seed.Action, Detail: seed.Detail, Status: StatusSucceeded}
	nextPending := Record{Seq: 7, OpID: "op-full", Actor: seed.Actor, Action: seed.Action, Detail: seed.Detail, Status: StatusPending}
	tf := frameEncodedLen(seedTerminal)
	budget := 3*tf + frameEncodedLen(nextPending) - 1

	sink := NewMemorySink(disk, budget, Faults{})

	// Pre-fill durable storage with three historical terminal records, as
	// left behind by earlier process instances.
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		rec := seedTerminal
		rec.Seq = uint64(i*2 + 2)
		rec.OpID = fmt.Sprintf("op-history-%d", i)
		if err := sink.Append(ctx, rec); err != nil {
			t.Fatalf("prefill %d: %v", i, err)
		}
	}

	c, err := New(context.Background(), sink, Config{MaxPending: 8})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	var ran atomic.Bool
	_, err = c.Commit(context.Background(), testOp("op-full"),
		func(context.Context) error { ran.Store(true); return nil })
	if !errors.Is(err, ErrSinkFull) {
		t.Fatalf("want ErrSinkFull, got %v", err)
	}
	if ran.Load() {
		t.Fatal("side effect ran even though no durable slot could be reserved")
	}

	// The failed reservation left no trace: only the seed op is known.
	if _, ok := c.Status("op-full"); ok {
		t.Fatal("rejected reservation must not be recorded as an operation")
	}
}

func TestDuplicateTerminalIsIdempotent(t *testing.T) {
	disk := NewMemoryDisk()
	c := newTestCommitter(t, disk, 4)

	var runs atomic.Int32
	effect := func(context.Context) error { runs.Add(1); return nil }

	first, err := c.Commit(context.Background(), testOp("dup"), effect)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	second, err := c.Commit(context.Background(), testOp("dup"), effect)
	if err != nil {
		t.Fatalf("duplicate commit: %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("effect ran %d times for one OpID", runs.Load())
	}
	if !second.Replayed || second.Status != StatusSucceeded || second.Seq != first.Seq {
		t.Fatalf("duplicate did not replay stored result: first=%+v second=%+v", first, second)
	}
}

func TestConcurrentDuplicatesShareOneSideEffect(t *testing.T) {
	disk := NewMemoryDisk()
	c := newTestCommitter(t, disk, 8)

	var runs atomic.Int32
	effect := func(context.Context) error {
		runs.Add(1)
		return nil
	}

	const waiters = 16
	var wg sync.WaitGroup
	errs := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Commit(context.Background(), testOp("race"), effect)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent duplicate: %v", err)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("effect ran %d times for concurrent duplicate OpIDs", runs.Load())
	}
}

func TestEffectFailureRecordsFailedResultUnderSameOpID(t *testing.T) {
	disk := NewMemoryDisk()
	c := newTestCommitter(t, disk, 4)

	effectErr := errors.New("upstream denied")
	res, err := c.Commit(context.Background(), testOp("op-fail"),
		func(context.Context) error { return effectErr })
	if !errors.Is(err, effectErr) {
		t.Fatalf("want effect error returned, got %v", err)
	}
	if res.Status != StatusFailed || !errors.Is(res.Failure, effectErr) {
		t.Fatalf("want failed result carrying the effect error, got %+v", res)
	}

	rec, ok := c.Status("op-fail")
	if !ok || rec.Status != StatusFailed || rec.Failure != effectErr.Error() {
		t.Fatalf("failure was not recorded under the same OpID: %+v", rec)
	}

	// Retrying the same OpID returns the stored failure; the effect is not
	// invoked again.
	var reruns atomic.Int32
	again, err := c.Commit(context.Background(), testOp("op-fail"),
		func(context.Context) error { reruns.Add(1); return nil })
	if err == nil || err.Error() != effectErr.Error() || !again.Replayed || reruns.Load() != 0 {
		t.Fatalf("failed OpID was re-executed: res=%+v err=%v reruns=%d", again, err, reruns.Load())
	}
}

func TestCrashAfterReservationDoesNotReexecute(t *testing.T) {
	disk := NewMemoryDisk()

	sink := NewMemorySink(disk, 0, Faults{})
	c, err := New(context.Background(), sink, Config{MaxPending: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Simulate a crash after the reservation is durable but before the side
	// effect can run: the AfterAppend fault fires on the Pending record.
	crashed := make(chan struct{})
	sink.faults.AfterAppend = func(rec Record) error {
		if rec.Status == StatusPending {
			disk.PowerLoss()
			close(crashed)
			return ErrClosed
		}
		return nil
	}

	var ran atomic.Bool
	_, commitErr := c.Commit(context.Background(), testOp("op-crash"),
		func(context.Context) error { ran.Store(true); return nil })
	<-crashed
	if !errors.Is(commitErr, ErrClosed) {
		t.Fatalf("want crash error, got %v", commitErr)
	}
	if ran.Load() {
		t.Fatal("side effect ran after the reservation crashed before acknowledgement")
	}

	// New process, same durable medium.
	reopened := disk.Reopen(Faults{})
	recovered, err := New(context.Background(), reopened, Config{MaxPending: 4})
	if err != nil {
		t.Fatalf("recover New: %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })

	pending := recovered.Pending()
	if len(pending) != 1 || pending[0] != "op-crash" {
		t.Fatalf("want one recovered pending op-crash, got %v", pending)
	}

	// A duplicate request must not trigger the effect; it gets ErrPending.
	_, err = recovered.Commit(context.Background(), testOp("op-crash"),
		func(context.Context) error { ran.Store(true); return nil })
	if !errors.Is(err, ErrPending) {
		t.Fatalf("want ErrPending after recovery, got %v", err)
	}
	if ran.Load() {
		t.Fatal("recovered pending operation was re-executed automatically")
	}

	// The host resolves it explicitly, still with no side effect.
	res, err := recovered.Resolve(context.Background(), "op-crash", nil)
	if err != nil || res.Status != StatusSucceeded {
		t.Fatalf("Resolve: %v res=%+v", err, res)
	}
	if len(recovered.Pending()) != 0 {
		t.Fatal("resolved operation still pending")
	}
}

func TestFaultBeforeAckLeavesNoAuditTrailAndNoEffect(t *testing.T) {
	disk := NewMemoryDisk()
	sink := NewMemorySink(disk, 0, Faults{
		BeforeAppend: func(Record) error { return errors.New("power lost before write") },
	})
	c, err := New(context.Background(), sink, Config{MaxPending: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var ran atomic.Bool
	_, err = c.Commit(context.Background(), testOp("op-lost"),
		func(context.Context) error { ran.Store(true); return nil })
	if err == nil {
		t.Fatal("want injection error, got nil")
	}
	if ran.Load() {
		t.Fatal("side effect ran without an acknowledged reservation")
	}

	reopened := disk.Reopen(Faults{})
	records, err := reopened.Replay(context.Background())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("unacknowledged reservation must not be durable, got %d records", len(records))
	}
}

func TestReservationIsDurableBeforeEffect(t *testing.T) {
	disk := NewMemoryDisk()
	sink := NewMemorySink(disk, 0, Faults{})
	c, err := New(context.Background(), sink, Config{MaxPending: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var seqAtEffect uint64
	_, err = c.Commit(context.Background(), testOp("op-order"),
		func(context.Context) error {
			records, replayErr := sink.Replay(context.Background())
			if replayErr != nil {
				return replayErr
			}
			if len(records) != 1 || records[0].Status != StatusPending || records[0].OpID != "op-order" {
				return fmt.Errorf("reservation not durable before effect: %+v", records)
			}
			seqAtEffect = records[0].Seq
			return nil
		})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if seqAtEffect != 1 {
		t.Fatalf("want reservation seq 1 visible inside effect, got %d", seqAtEffect)
	}
}
