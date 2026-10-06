// Package storetest is a persistence-model-independent crash-recovery
// suite. Any inbox.Store implementation is expected to pass it, proving
// the two core guarantees under crash and redelivery:
//
//   - business effect exactly once (dedup + state + outbox commit
//     atomically in one local transaction);
//   - network send at least once with a STABLE output id (a resend after
//     a lost ack is deduplicated by the receiver).
package storetest

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"testing"

	"inbox"
)

// Opener opens a fresh or recovered store on dir.
type Opener func(dir string) (inbox.Store, error)

// crash is panicked by crash-injection hooks to simulate process death.
// The store under test is write-through, so simply abandoning the
// instance and reopening from the same directory models a crash.
type crash struct{}

func recoverCrash(t *testing.T) {
	if r := recover(); r != nil {
		if _, ok := r.(crash); !ok {
			panic(r)
		}
	}
}

// receiver models the downstream consumer: it dedups by the stable
// output id, so its business effect is applied exactly once even when
// the network delivers the same message twice.
type receiver struct {
	sentIDs []string
	seen    map[string]bool
	applied int
}

func newReceiver() *receiver { return &receiver{seen: make(map[string]bool)} }

func (r *receiver) Send(ctx context.Context, msg inbox.OutboxMessage) error {
	r.sentIDs = append(r.sentIDs, msg.ID)
	if !r.seen[msg.ID] {
		r.seen[msg.ID] = true
		r.applied++
	}
	return nil
}

// counterEffect is the business handler used across the suite: it
// increments a counter in business state and enqueues one output whose
// id is deterministically derived from the event id.
func counterEffect(ev inbox.Event) func(tx inbox.Tx) error {
	return func(tx inbox.Tx) error {
		raw, _, err := tx.GetState("count")
		if err != nil {
			return err
		}
		n, _ := strconv.Atoi(string(raw))
		if err := tx.PutState("count", []byte(strconv.Itoa(n+1))); err != nil {
			return err
		}
		return tx.EnqueueOutbox(inbox.OutboxMessage{
			ID:      inbox.OutputID(ev.ID, 0),
			EventID: ev.ID,
			Topic:   "counted",
			Payload: []byte(ev.ID),
		})
	}
}

func readCount(t *testing.T, s inbox.Store) int {
	t.Helper()
	n := -1
	err := s.View(context.Background(), func(tx inbox.Tx) error {
		raw, ok, err := tx.GetState("count")
		if err != nil {
			return err
		}
		if !ok {
			n = 0
			return nil
		}
		v, err := strconv.Atoi(string(raw))
		n = v
		return err
	})
	if err != nil {
		t.Fatalf("read count: %v", err)
	}
	return n
}

func pendingCount(t *testing.T, s inbox.Store) int {
	t.Helper()
	n := -1
	err := s.View(context.Background(), func(tx inbox.Tx) error {
		msgs, err := tx.PendingOutbox()
		n = len(msgs)
		return err
	})
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	return n
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "inbox-storetest-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	return dir
}

// Run executes the full suite against the given persistence model.
func Run(t *testing.T, open Opener) {
	t.Run("DuplicateDeliveryAppliesEffectOnce", func(t *testing.T) {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		s, err := open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer s.Close()
		p := &inbox.Processor{Store: s}
		ev := inbox.Event{ID: "evt-1", Type: "counted"}
		for i := 0; i < 3; i++ { // broker redelivers the same event
			if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
				t.Fatalf("handle #%d: %v", i, err)
			}
		}
		if got := readCount(t, s); got != 1 {
			t.Fatalf("business effect applied %d times, want 1", got)
		}
		if got := pendingCount(t, s); got != 1 {
			t.Fatalf("pending outputs = %d, want 1", got)
		}
	})

	t.Run("CrashBeforeCommitLeavesNoTrace", func(t *testing.T) {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		s, err := open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		p := &inbox.Processor{Store: s}
		ev := inbox.Event{ID: "evt-1", Type: "counted"}
		// Handler mutates state and enqueues output, then "crashes"
		// (returns error) before commit: the whole tx must roll back.
		err = p.Handle(context.Background(), ev, func(tx inbox.Tx) error {
			if err := counterEffect(ev)(tx); err != nil {
				return err
			}
			return fmt.Errorf("simulated crash before commit")
		})
		if err == nil {
			t.Fatalf("expected handler error")
		}
		s.Close()
		s, err = open(dir) // recover
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer s.Close()
		if got := readCount(t, s); got != 0 {
			t.Fatalf("rolled-back state visible: count = %d, want 0", got)
		}
		if got := pendingCount(t, s); got != 0 {
			t.Fatalf("rolled-back output visible: pending = %d, want 0", got)
		}
		// The event was never committed, so redelivery must be processed.
		p = &inbox.Processor{Store: s}
		if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
			t.Fatalf("rehandle: %v", err)
		}
		if got := readCount(t, s); got != 1 {
			t.Fatalf("count after rehandle = %d, want 1", got)
		}
	})

	t.Run("CrashAfterCommitBeforeSend", func(t *testing.T) {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		s, err := open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		p := &inbox.Processor{Store: s}
		ev := inbox.Event{ID: "evt-1", Type: "counted"}
		if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
			t.Fatalf("handle: %v", err)
		}
		// Crash after the tx committed but before anything was sent.
		s.Close()
		s, err = open(dir)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer s.Close()
		if got := readCount(t, s); got != 1 {
			t.Fatalf("committed state lost: count = %d, want 1", got)
		}
		if got := pendingCount(t, s); got != 1 {
			t.Fatalf("committed output lost: pending = %d, want 1", got)
		}
		// Redelivery after recovery is still a no-op.
		p = &inbox.Processor{Store: s}
		if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
			t.Fatalf("rehandle: %v", err)
		}
		if got := readCount(t, s); got != 1 {
			t.Fatalf("duplicate after recovery changed state: count = %d", got)
		}
		// Recovery dispatch sends the committed output exactly once.
		r := newReceiver()
		d := &inbox.Dispatcher{Store: s, Sender: r}
		n, err := d.DispatchPending(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n != 1 || r.applied != 1 {
			t.Fatalf("sent = %d, receiver applied = %d, want 1/1", n, r.applied)
		}
		if got := pendingCount(t, s); got != 0 {
			t.Fatalf("pending after dispatch = %d, want 0", got)
		}
	})

	t.Run("CrashAfterSendBeforeMarkSentResendsWithStableID", func(t *testing.T) {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		s, err := open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		p := &inbox.Processor{Store: s}
		ev := inbox.Event{ID: "evt-1", Type: "counted"}
		if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
			t.Fatalf("handle: %v", err)
		}
		r := newReceiver()
		// First dispatch: send succeeds, then the process "crashes"
		// before the sent-mark is persisted (the ack is lost).
		d := &inbox.Dispatcher{Store: s, Sender: r}
		d.BeforeMarkSent = func() { panic(crash{}) }
		func() {
			defer recoverCrash(t)
			d.DispatchPending(context.Background())
		}()
		s.Close()
		s, err = open(dir) // recover: sent-mark was lost
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer s.Close()
		if got := pendingCount(t, s); got != 1 {
			t.Fatalf("pending after crash = %d, want 1 (mark lost)", got)
		}
		// Recovery resends. The id must be identical.
		d = &inbox.Dispatcher{Store: s, Sender: r}
		if _, err := d.DispatchPending(context.Background()); err != nil {
			t.Fatalf("redispatch: %v", err)
		}
		if len(r.sentIDs) != 2 {
			t.Fatalf("network sends = %d, want 2 (resend allowed)", len(r.sentIDs))
		}
		if r.sentIDs[0] != r.sentIDs[1] {
			t.Fatalf("output id not stable across resend: %q vs %q", r.sentIDs[0], r.sentIDs[1])
		}
		if r.applied != 1 {
			t.Fatalf("receiver effect applied %d times, want 1 (dedup by id)", r.applied)
		}
		if got := readCount(t, s); got != 1 {
			t.Fatalf("business effect applied %d times, want 1", got)
		}
	})

	t.Run("FailedSendRetriesWithStableID", func(t *testing.T) {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		s, err := open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer s.Close()
		p := &inbox.Processor{Store: s}
		ev := inbox.Event{ID: "evt-1", Type: "counted"}
		if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
			t.Fatalf("handle: %v", err)
		}
		r := newReceiver()
		flaky := &flakySender{inner: r, failures: 2}
		d := &inbox.Dispatcher{Store: s, Sender: flaky}
		for i := 0; i < 2; i++ {
			if _, err := d.DispatchPending(context.Background()); err == nil {
				t.Fatalf("attempt %d: expected send error", i)
			}
		}
		if _, err := d.DispatchPending(context.Background()); err != nil {
			t.Fatalf("final attempt: %v", err)
		}
		if len(r.sentIDs) != 1 {
			t.Fatalf("receiver got %d sends, want 1", len(r.sentIDs))
		}
		if r.sentIDs[0] != inbox.OutputID(ev.ID, 0) {
			t.Fatalf("output id %q does not match deterministic id", r.sentIDs[0])
		}
	})

	t.Run("MarkSentSurvivesRecovery", func(t *testing.T) {
		dir := tempDir(t)
		defer os.RemoveAll(dir)
		s, err := open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		p := &inbox.Processor{Store: s}
		ev := inbox.Event{ID: "evt-1", Type: "counted"}
		if err := p.Handle(context.Background(), ev, counterEffect(ev)); err != nil {
			t.Fatalf("handle: %v", err)
		}
		r := newReceiver()
		d := &inbox.Dispatcher{Store: s, Sender: r}
		if _, err := d.DispatchPending(context.Background()); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		s.Close()
		s, err = open(dir) // recover after clean mark
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer s.Close()
		n, err := d.DispatchPending(context.Background())
		if err != nil {
			t.Fatalf("redispatch: %v", err)
		}
		if n != 0 || len(r.sentIDs) != 1 {
			t.Fatalf("resent after persisted mark: sends = %d", len(r.sentIDs))
		}
	})
}

type flakySender struct {
	inner    inbox.Sender
	failures int
}

func (f *flakySender) Send(ctx context.Context, msg inbox.OutboxMessage) error {
	if f.failures > 0 {
		f.failures--
		return fmt.Errorf("simulated network failure")
	}
	return f.inner.Send(ctx, msg)
}
