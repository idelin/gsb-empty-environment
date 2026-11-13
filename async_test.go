package netsec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ns "example.org/netsec/q0113"
)

func TestSpawnRequiresExplicitPolicy(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	callback := func(context.Context, *ns.TaskContext) error { return nil }

	if _, err := f.host.SpawnAsync(req, ns.PolicyUnspecified, callback); !errors.Is(err, ns.ErrPolicyRequired) {
		t.Fatalf("unspecified policy error = %v", err)
	}
	if _, err := f.host.SpawnAsync(req, ns.SpawnPolicy(99), callback); !errors.Is(err, ns.ErrInvalidPolicy) {
		t.Fatalf("invalid policy error = %v", err)
	}
	if _, err := f.host.SpawnAsync(req, ns.NoIdentity, nil); !errors.Is(err, ns.ErrNilCallback) {
		t.Fatalf("nil callback error = %v", err)
	}
	if _, err := f.host.SpawnAsync(req, ns.NoIdentity, callback, ns.CaptureAtLevel(ns.Impersonation, nil)); !errors.Is(err, ns.ErrNoIdentityOption) {
		t.Fatalf("NoIdentity options error = %v", err)
	}
}

func TestCaptureDefaultsToIdentification(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	task, err := f.host.SpawnAsync(req, ns.Capture, func(_ context.Context, ctx *ns.TaskContext) error {
		snap := ctx.Identity()
		if snap.Level() != ns.Identification {
			t.Fatalf("capture level = %s, want Identification", snap.Level())
		}
		if snap.RequestID() != req.ID() || snap.Generation() != req.Generation() {
			t.Fatalf("capture binding = %+v", snap)
		}
		if _, err := ctx.ReadIdentity(); err != nil {
			t.Fatalf("ReadIdentity: %v", err)
		}
		return ctx.Local(func() error {
			t.Fatal("Identification capture reached a local resource")
			return nil
		})
	})
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	if task.Policy() != ns.Capture || task.Generation() != req.Generation() ||
		task.RequestID() != req.ID() || task.Deadline() != req.Deadline() {
		t.Fatalf("task binding: %+v", task)
	}
	requireError(t, task.Run(context.Background()), ns.ErrIdentityCannotAccessResources)
	requireError(t, task.Verify(), ns.ErrTaskFinished)
}

func TestNoIdentityTaskHasNoAccess(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	task, err := f.host.SpawnAsync(req, ns.NoIdentity, func(_ context.Context, ctx *ns.TaskContext) error {
		if !ctx.Identity().IsZero() || ctx.HasIdentity() {
			t.Fatalf("NoIdentity exposed snapshot: %+v", ctx.Identity())
		}
		return ctx.Remote(func() error {
			t.Fatal("NoIdentity reached a remote resource")
			return nil
		})
	})
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	requireError(t, task.Run(context.Background()), ns.ErrNoIdentity)
}

func TestElevatedCaptureNeedsMatchingGrantAndBoundedDeadline(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	callback := func(_ context.Context, ctx *ns.TaskContext) error {
		return ctx.Remote(func() error { return nil })
	}

	if _, err := f.host.SpawnAsync(req, ns.Capture, callback, ns.CaptureAtLevel(ns.Delegation, nil)); !errors.Is(err, ns.ErrGrantRequired) {
		t.Fatalf("missing grant error = %v", err)
	}

	weak, err := f.host.Authorize(req, ns.Impersonation, req.Deadline())
	if err != nil {
		t.Fatalf("Authorize weak: %v", err)
	}
	if _, err := f.host.SpawnAsync(req, ns.Capture, callback, ns.CaptureAtLevel(ns.Delegation, weak)); !errors.Is(err, ns.ErrGrantInsufficient) {
		t.Fatalf("insufficient grant error = %v", err)
	}

	short := req.Deadline().Add(-time.Second)
	shortGrant, err := f.host.Authorize(req, ns.Delegation, short)
	if err != nil {
		t.Fatalf("Authorize short: %v", err)
	}
	if _, err := f.host.SpawnAsync(req, ns.Capture, callback, ns.CaptureAtLevel(ns.Delegation, shortGrant)); !errors.Is(err, ns.ErrDeadlineExceedsGrant) {
		t.Fatalf("unbounded task deadline error = %v", err)
	}
	if _, err := f.host.SpawnAsync(req, ns.Capture, callback,
		ns.CaptureAtLevel(ns.Delegation, shortGrant), ns.WithTaskDeadline(short)); err != nil {
		t.Fatalf("bounded elevated capture: %v", err)
	}

	full, err := f.host.Authorize(req, ns.Delegation, req.Deadline())
	if err != nil {
		t.Fatalf("Authorize full: %v", err)
	}
	task, err := f.host.SpawnAsync(req, ns.Capture, func(_ context.Context, ctx *ns.TaskContext) error {
		if ctx.Identity().Level() != ns.Delegation {
			t.Fatalf("elevated capture level = %s", ctx.Identity().Level())
		}
		return ctx.Remote(func() error { return nil })
	}, ns.CaptureAtLevel(ns.Delegation, full))
	if err != nil {
		t.Fatalf("SpawnAsync elevated: %v", err)
	}
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run elevated: %v", err)
	}

	if _, err := f.host.SpawnAsync(req, ns.Capture, callback,
		ns.WithTaskDeadline(req.Deadline().Add(time.Nanosecond))); !errors.Is(err, ns.ErrDeadlineExceedsRequest) {
		t.Fatalf("extended task deadline error = %v", err)
	}
}

func TestCaptureCannotEscalateSourceLevel(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	grant, err := f.host.Authorize(req, ns.Impersonation, req.Deadline())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	callback := func(context.Context, *ns.TaskContext) error { return nil }
	if _, err := f.host.SpawnAsync(req, ns.Capture, callback, ns.CaptureAtLevel(ns.Delegation, grant)); !errors.Is(err, ns.ErrCaptureEscalation) {
		t.Fatalf("escalation error = %v", err)
	}
}

func TestRequestEndAndConnectionReuseRevokeTasks(t *testing.T) {
	f := newFixture()
	conn, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	called := 0
	callback := func(context.Context, *ns.TaskContext) error {
		called++
		return nil
	}
	t1, err := f.host.SpawnAsync(req, ns.Capture, callback)
	if err != nil {
		t.Fatalf("SpawnAsync t1: %v", err)
	}
	t2, err := f.host.SpawnAsync(req, ns.Capture, callback)
	if err != nil {
		t.Fatalf("SpawnAsync t2: %v", err)
	}
	if err := req.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	requireError(t, t1.Run(context.Background()), ns.ErrTaskRevoked)
	requireError(t, t2.Run(context.Background()), ns.ErrTaskRevoked)
	if called != 0 {
		t.Fatalf("revoked callbacks ran %d times", called)
	}

	if err := conn.Reuse(); err != nil {
		t.Fatalf("Reuse: %v", err)
	}
	r2 := f.requestFor(t, conn, "r2", ns.Delegation, deadlineNow(f))
	if r2.Generation() <= req.Generation() {
		t.Fatal("generation did not advance")
	}
	requireError(t, t1.Verify(), ns.ErrTaskRevoked)
	requireError(t, req.Token().Valid(), ns.ErrRequestEnded)
}

func TestCallbackStartRevalidatesHandle(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	called := false
	task, err := f.host.SpawnAsync(req, ns.Capture, func(context.Context, *ns.TaskContext) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	if err := req.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	requireError(t, task.Run(context.Background()), ns.ErrTaskRevoked)
	if called {
		t.Fatal("callback ran after request end")
	}
}

func TestTaskDeadlineAndCancellationAtStart(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	task, err := f.host.SpawnAsync(req, ns.Capture,
		func(context.Context, *ns.TaskContext) error {
			t.Fatal("expired callback ran")
			return nil
		},
		ns.WithTaskDeadline(f.clock.Now().Add(50*time.Millisecond)))
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	f.clock.Advance(50 * time.Millisecond)
	requireError(t, task.Run(context.Background()), ns.ErrTaskExpired)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	task2, err := f.host.SpawnAsync(req, ns.Capture, func(context.Context, *ns.TaskContext) error {
		t.Fatal("canceled callback ran")
		return nil
	})
	if err != nil {
		t.Fatalf("SpawnAsync task2: %v", err)
	}
	if err := task2.Run(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start error = %v", err)
	}
	requireError(t, task2.Run(context.Background()), ns.ErrTaskFinished)
}

func TestEveryResourceCallRevalidatesHandle(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	grant, err := f.host.Authorize(req, ns.Delegation, req.Deadline())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	var secondErr error
	task, err := f.host.SpawnAsync(req, ns.Capture, func(_ context.Context, ctx *ns.TaskContext) error {
		if err := ctx.Remote(func() error { return nil }); err != nil {
			return err
		}
		if err := req.End(); err != nil {
			return err
		}
		secondErr = ctx.Remote(func() error {
			t.Fatal("second remote call ran after request end")
			return nil
		})
		return nil
	}, ns.CaptureAtLevel(ns.Delegation, grant))
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	requireError(t, task.Run(context.Background()), ns.ErrTaskRevoked)
	requireError(t, secondErr, ns.ErrTaskRevoked)

	_, req2 := f.request(t, "c2", "r2", ns.Impersonation, deadlineNow(f))
	var escaped *ns.TaskContext
	clean, err := f.host.SpawnAsync(req2, ns.Capture, func(_ context.Context, ctx *ns.TaskContext) error {
		escaped = ctx
		return nil
	})
	if err != nil {
		t.Fatalf("SpawnAsync clean: %v", err)
	}
	if err := clean.Run(context.Background()); err != nil {
		t.Fatalf("Run clean: %v", err)
	}
	requireError(t, escaped.Local(func() error {
		t.Fatal("escaped context reached a resource after callback return")
		return nil
	}), ns.ErrTaskFinished)
}

func TestTaskExpiresDuringCallback(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	task, err := f.host.SpawnAsync(req, ns.Capture, func(_ context.Context, ctx *ns.TaskContext) error {
		f.clock.Advance(100 * time.Millisecond)
		return ctx.Local(func() error { return nil })
	}, ns.WithTaskDeadline(f.clock.Now().Add(100*time.Millisecond)))
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	requireError(t, task.Run(context.Background()), ns.ErrTaskExpired)
}

func TestTaskPanicLeavesTerminalState(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Impersonation, deadlineNow(f))
	task, err := f.host.SpawnAsync(req, ns.Capture, func(context.Context, *ns.TaskContext) error {
		panic("boom")
	})
	if err != nil {
		t.Fatalf("SpawnAsync: %v", err)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("expected callback panic")
			}
		}()
		_ = task.Run(context.Background())
	}()
	if !task.Done() {
		t.Fatal("panicking task is not terminal")
	}
	requireError(t, task.Run(context.Background()), ns.ErrTaskFinished)
}

func TestSpawnOnThreadCapturesOnlyCurrentRequest(t *testing.T) {
	f := newFixture()
	_, r1 := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	_, r2 := f.request(t, "c2", "r2", ns.Delegation, deadlineNow(f))
	thread := f.host.NewThread()
	if err := thread.Enter(r1.Token()); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	callback := func(context.Context, *ns.TaskContext) error { return nil }
	task, err := f.host.SpawnAsyncOn(thread, r1, ns.Capture, callback)
	if err != nil {
		t.Fatalf("SpawnAsyncOn: %v", err)
	}
	if task.Identity().RequestID() != r1.ID() || task.Identity().Level() != ns.Identification {
		t.Fatalf("Spawning capture: %+v", task.Identity())
	}
	if _, err := f.host.SpawnAsyncOn(thread, r2, ns.Capture, callback); !errors.Is(err, ns.ErrWrongRequest) {
		t.Fatalf("other request spawn error = %v", err)
	}
}

func TestCaptureCannotOutliveSourceToken(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	nestedDeadline := f.clock.Now().Add(25 * time.Millisecond)
	nested, err := f.auth.IssueNested(req, ns.Impersonation, nestedDeadline)
	if err != nil {
		t.Fatalf("IssueNested: %v", err)
	}
	thread := f.host.NewThread()
	if err := thread.Enter(nested); err != nil {
		t.Fatalf("Enter nested: %v", err)
	}
	callback := func(context.Context, *ns.TaskContext) error { return nil }

	task, err := f.host.SpawnAsyncOn(thread, req, ns.Capture, callback)
	if err != nil {
		t.Fatalf("SpawnAsyncOn: %v", err)
	}
	if task.Deadline() != nestedDeadline || task.Identity().Deadline() != nestedDeadline {
		t.Fatalf("capture extended source deadline: task=%v snapshot=%v source=%v",
			task.Deadline(), task.Identity().Deadline(), nestedDeadline)
	}
	if _, err := f.host.SpawnAsyncOn(thread, req, ns.Capture, callback,
		ns.WithTaskDeadline(req.Deadline())); !errors.Is(err, ns.ErrDeadlineExceedsToken) {
		t.Fatalf("extended capture error = %v", err)
	}
}
