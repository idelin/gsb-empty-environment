package netsec_test

import (
	"context"
	"testing"

	ns "example.org/netsec/q0113"
)

// TestLegacyAuditTraceIsDenied replays the essential shape of the audit
// counterexample against the safe component: nested capture, request end,
// connection/thread reuse, and a later callback start. The callback must be
// refused before it can run and the old token must be dead.
func TestLegacyAuditTraceIsDenied(t *testing.T) {
	f := newFixture()
	conn, r1 := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	thread := f.host.NewThread()
	if err := thread.Enter(r1.Token()); err != nil {
		t.Fatalf("Enter R1: %v", err)
	}
	nested, err := f.auth.IssueNested(r1, ns.Identification, r1.Deadline())
	if err != nil {
		t.Fatalf("IssueNested: %v", err)
	}
	err = thread.RunScoped(nested, func() error {
		return f.host.CheckThreadAccess(thread, ns.LocalResource)
	})
	requireError(t, err, ns.ErrIdentityCannotAccessResources)

	ran := false
	task, err := f.host.SpawnAsyncOn(thread, r1, ns.Capture, func(context.Context, *ns.TaskContext) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("SpawnAsyncOn: %v", err)
	}
	if task.Identity().Level() != ns.Identification {
		t.Fatalf("capture level = %s", task.Identity().Level())
	}
	if err := thread.Exit(r1.Token()); err != nil {
		t.Fatalf("Exit R1: %v", err)
	}
	if err := r1.End(); err != nil {
		t.Fatalf("End R1: %v", err)
	}
	if err := conn.Reuse(); err != nil {
		t.Fatalf("Reuse: %v", err)
	}

	r2 := f.requestFor(t, conn, "r2", ns.Delegation, deadlineNow(f))
	if err := thread.Enter(r2.Token()); err != nil {
		t.Fatalf("Enter R2: %v", err)
	}
	if err := task.Run(context.Background()); err == nil {
		t.Fatal("stale task started on the reused thread")
	}
	if ran {
		t.Fatal("stale callback ran with another request's thread identity")
	}
	requireError(t, f.host.CheckAccess(r1, r1.Token(), ns.LocalResource), ns.ErrRequestEnded)
	requireError(t, thread.Enter(r1.Token()), ns.ErrRequestEnded)
}
