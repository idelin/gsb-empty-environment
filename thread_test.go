package netsec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ns "example.org/netsec/q0113"
)

func TestEnterRequiresCurrentRequest(t *testing.T) {
	f := newFixture()
	_, r1 := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	_, r2 := f.request(t, "c2", "r2", ns.Delegation, deadlineNow(f))
	_, r3 := f.request(t, "c3", "r3", ns.Delegation, deadlineNow(f))

	thread := f.host.NewThread()
	if err := thread.Bind(r1); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	requireError(t, thread.Enter(r2.Token()), ns.ErrWrongRequest)
	if err := thread.Enter(r1.Token()); err != nil {
		t.Fatalf("Enter R1: %v", err)
	}
	requireError(t, thread.EnterFor(r2, r1.Token()), ns.ErrTokenRequestMismatch)
	if err := thread.Exit(r1.Token()); err != nil {
		t.Fatalf("Exit R1: %v", err)
	}

	other := f.host.NewThread()
	if err := other.Enter(r2.Token()); err != nil {
		t.Fatalf("unbound Enter should bind: %v", err)
	}
	if other.BoundRequest() != r2 {
		t.Fatal("Enter did not bind the thread to the token request")
	}
	requireError(t, other.Enter(r3.Token()), ns.ErrWrongRequest)

	if err := r1.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	requireError(t, thread.Enter(r1.Token()), ns.ErrRequestEnded)

	nested, err := f.auth.IssueNested(r2, ns.Identification, r2.Deadline())
	if err != nil {
		t.Fatalf("IssueNested: %v", err)
	}
	if err := other.Enter(nested); err != nil {
		t.Fatalf("Enter nested: %v", err)
	}
	if err := other.Exit(nested); err != nil {
		t.Fatalf("Exit nested: %v", err)
	}
}

func TestExitIsExactAndRevertToSelfRefusesOuterLoss(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	outer := req.Token()
	inner, err := f.auth.IssueNested(req, ns.Impersonation, req.Deadline())
	if err != nil {
		t.Fatalf("IssueNested inner: %v", err)
	}
	innermost, err := f.auth.IssueNested(req, ns.Identification, req.Deadline())
	if err != nil {
		t.Fatalf("IssueNested innermost: %v", err)
	}
	thread := f.host.NewThread()
	for _, tok := range []*ns.Token{outer, inner, innermost} {
		if err := thread.Enter(tok); err != nil {
			t.Fatalf("Enter: %v", err)
		}
	}
	requireError(t, thread.Exit(outer), ns.ErrStackMismatch)
	requireError(t, thread.Exit(inner), ns.ErrStackMismatch)
	if thread.Depth() != 3 {
		t.Fatalf("mismatched Exit changed depth: %d", thread.Depth())
	}
	requireError(t, thread.RevertToSelf(), ns.ErrNestedImpersonation)
	if thread.Depth() != 3 {
		t.Fatalf("failed RevertToSelf changed depth: %d", thread.Depth())
	}
	if err := thread.Exit(innermost); err != nil {
		t.Fatalf("Exit innermost: %v", err)
	}
	if err := thread.Exit(inner); err != nil {
		t.Fatalf("Exit inner: %v", err)
	}
	if err := thread.RevertToSelf(); err != nil {
		t.Fatalf("RevertToSelf single: %v", err)
	}
	if thread.Depth() != 0 {
		t.Fatalf("RevertToSelf left depth %d", thread.Depth())
	}
	requireError(t, thread.Exit(outer), ns.ErrNoImpersonation)
}

func TestPanicAndCancellationRestoreOuterStack(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	outer := req.Token()
	inner, err := f.auth.IssueNested(req, ns.Impersonation, req.Deadline())
	if err != nil {
		t.Fatalf("IssueNested: %v", err)
	}
	thread := f.host.NewThread()
	if err := thread.Enter(outer); err != nil {
		t.Fatalf("Enter outer: %v", err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = thread.RunScoped(inner, func() error {
			if thread.Depth() != 2 {
				t.Fatalf("inside scope depth = %d", thread.Depth())
			}
			panic("callback failed")
		})
	}()
	if thread.Depth() != 1 || thread.Current().ID() != outer.ID() {
		t.Fatalf("panic did not restore outer stack: depth=%d current=%v", thread.Depth(), thread.Current())
	}

	ctx, cancel := context.WithCancel(context.Background())
	err = thread.RunScopedContext(ctx, inner, func(context.Context) error {
		if thread.Depth() != 2 {
			t.Fatalf("inside canceled scope depth = %d", thread.Depth())
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if thread.Depth() != 1 || thread.Current().ID() != outer.ID() {
		t.Fatalf("cancel did not restore outer stack: depth=%d current=%v", thread.Depth(), thread.Current())
	}

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	err = thread.RunScopedContext(canceled, inner, func(context.Context) error {
		t.Fatal("callback ran with a pre-canceled context")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel error = %v", err)
	}
	if thread.Depth() != 1 {
		t.Fatalf("pre-cancel changed depth: %d", thread.Depth())
	}

	// A nested frame pushed and left behind inside the callback is also
	// removed by the scope restore.
	if err := thread.RunScoped(inner, func() error {
		return thread.Enter(inner)
	}); err != nil {
		t.Fatalf("RunScoped nested: %v", err)
	}
	if thread.Depth() != 1 {
		t.Fatalf("normal scope restore left depth %d", thread.Depth())
	}
}

func TestThreadPoolReuseAndGenerationRevocation(t *testing.T) {
	f := newFixture()
	conn, r1 := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	thread := f.host.NewThread()

	if err := thread.Bind(r1); err != nil {
		t.Fatalf("Bind R1: %v", err)
	}
	if err := thread.RunScoped(r1.Token(), func() error {
		return f.host.AccessFromThread(thread, ns.LocalResource, func() error { return nil })
	}); err != nil {
		t.Fatalf("R1 local access: %v", err)
	}
	if thread.Depth() != 0 {
		t.Fatalf("scope left depth %d", thread.Depth())
	}

	if err := r1.End(); err != nil {
		t.Fatalf("End R1: %v", err)
	}
	if err := conn.Reuse(); err != nil {
		t.Fatalf("Reuse: %v", err)
	}
	r2 := f.requestFor(t, conn, "r2", ns.Impersonation, deadlineNow(f))
	if r2.Generation() <= r1.Generation() {
		t.Fatalf("generation did not advance")
	}
	if err := thread.Bind(r2); err != nil {
		t.Fatalf("Bind R2: %v", err)
	}
	if err := thread.RunScoped(r2.Token(), func() error {
		return f.host.AccessFromThread(thread, ns.LocalResource, func() error { return nil })
	}); err != nil {
		t.Fatalf("R2 local access: %v", err)
	}
	requireError(t, f.host.CheckAccess(r1, r1.Token(), ns.LocalResource), ns.ErrRequestEnded)
	requireError(t, thread.Enter(r1.Token()), ns.ErrRequestEnded)
}

func TestThreadAccessRequiresBindingAndLiveTop(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	thread := f.host.NewThread()
	requireError(t, f.host.CheckThreadAccess(thread, ns.LocalResource), ns.ErrNoCurrentRequest)
	if err := thread.Enter(req.Token()); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if err := f.host.CheckThreadAccess(thread, ns.LocalResource); err != nil {
		t.Fatalf("thread access: %v", err)
	}
	if err := req.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	requireError(t, f.host.CheckThreadAccess(thread, ns.LocalResource), ns.ErrRequestEnded)
}

func TestDeadlineUsesMonotonicClock(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, f.clock.Now().Add(10*time.Second))
	if err := f.host.CheckAccess(req, req.Token(), ns.LocalResource); err != nil {
		t.Fatalf("fresh access: %v", err)
	}
	f.clock.Advance(10 * time.Second)
	requireError(t, f.host.CheckAccess(req, req.Token(), ns.LocalResource), ns.ErrTokenExpired)
}
